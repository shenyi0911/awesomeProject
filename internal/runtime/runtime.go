// Package runtime 算法运行时编排器（即插即用 / drop-in 执行层）。
//
// 职责（与 awesomeProject 管理平面分离的关注点，但现合并进同一进程）：
//
//	· 算法文件以「版本」为单位存储在 MinIO（对象存储），平台是真相源；运行时不依赖任何固定目录
//	· 启动时按「最新版本=released」的算法登记并拉起；其余留待「发布」动作触发拉起
//	· 拉起时：从 MinIO 下载该已发布版本的全部文件到本地暂存目录(workspace/<code>/<version>/)，
//	  解析其中的 algorithm.yaml 得到启动契约(entry/port/health)，再用 dapr run 拉起进程
//	· 「发布」动作触发拉起该算法进程；「归档」动作在算法已无已发布版本时回收其进程并清理暂存
//	· 计算类入口转发前做「发布状态闸门」：最新版本状态非 released 则 fail-closed 拒绝调用
//	· 进程退出时统一回收（killTree），不引入空闲回收 / 热监听 / 懒拉起等动态生命周期
//
// 转发逻辑对所有算法一致（同一句 invoke/{app-id}/method/calculate）。
// 零第三方依赖（仅标准库 + 注入的 VersionResolver / BlobDownloader 接口）。需要本机安装 dapr。
package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// statusReleased 已发布状态（与 platform version.VersionStatusReleased 取值一致）。
const statusReleased = "released"

// AlgoInstance 单个算法的运行实例。
type AlgoInstance struct {
	Code         string // 算法 code（平台路由键，也是 dapr app-id 的来源）
	Manifest     *Manifest
	Dir          string // 本地暂存目录（从 MinIO 抽取的已发布版本文件）
	StagingDir   string // 同上，用于归档时清理
	Version      string // 当前拉起的版本号
	VersionID    string // 当前拉起版本的版本ID（平台版本表主键）
	AppPort      int
	DaprHTTPPort int
	DaprGRPCPort int
	Cmd          *exec.Cmd
	Healthy      atomic.Bool // 进程是否已就绪（健康检查通过）
	launching    atomic.Bool // 是否正在拉起（重入保护）
}

// FileRef 版本文件在对象存储中的引用。
type FileRef struct {
	Key     string // MinIO 对象 key（如 versions/{vid}/{uuid}/{rel}）
	RelPath string // 还原目录结构用的相对路径（如 algorithm.yaml 或 src/app.py）
}

// VersionResolver 平台为运行时提供的「版本真相源」访问能力（从 MinIO 拉起所需）。
// 由仓储层（*sqlVersionRepo）实现，main.go 注入。
type VersionResolver interface {
	// LatestReleasedVersion 返回该 code 最新版本的 (versionID, version, status)。
	// 若最新版本为 released 则 versionID 非空；否则 versionID 为空串（status 为最新实际状态）。
	LatestReleasedVersion(ctx context.Context, code string) (versionID, version, status string, err error)
	// VersionFiles 返回某版本的 MinIO 文件清单（key + 相对路径）。
	VersionFiles(ctx context.Context, versionID string) ([]FileRef, error)
	// ListReleasedCodes 返回「最新版本为 released」的所有算法 code（用于启动时恢复拉起）。
	ListReleasedCodes(ctx context.Context) ([]string, error)
}

// BlobDownloader 对象存储下载能力（由 filestorage.FileStorage 满足）。
type BlobDownloader interface {
	Download(ctx context.Context, key string) (io.ReadCloser, error)
}

// PortResolver 由平台算法仓储实现，供运行时按 code 取算法监听端口。
// 端口不再由用户指定，改为平台在拉起时按本机空闲端口动态分配并持久化（SetPort）。
type PortResolver interface {
	PortForCode(ctx context.Context, code string) (int, error)
	SetPort(ctx context.Context, code string, port int) error
}

// Options 编排器配置（由 main.go 从 config + 环境变量构造）。
type Options struct {
	Workspace           string // 本地暂存根目录，抽取已发布版本文件用；默认用户缓存目录（源码树之外），可用 RUNTIME_WORKSPACE 覆盖
	DaprBin             string
	DefaultAlgo         string
	ReleaseCheckEnabled bool
	ReleaseCacheTTL     time.Duration
	StatusQuerier       StatusQuerier   // 发布状态闸门（按 code 查最新版本状态）
	Resolver            VersionResolver // 拉起时解析已发布版本 + 文件清单
	Downloader          BlobDownloader  // 从 MinIO 下载文件字节
	PortResolver        PortResolver    // 按 code 取平台分配的算法监听端口（拉起时按本机动态分配并持久化）
}

// DefaultWorkspace 返回暂存根目录默认值：用户缓存目录下的 algorithm-platform\runtime-workspace
// （Windows 为 %LocalAppData%\algorithm-platform\runtime-workspace）。
// 刻意不放源码树内：暂存内容全部是用户上传的算法文件，属于运行时数据而非平台源码；
// 删除无数据损失，下次调用该算法时会从 MinIO 重新下载。
func DefaultWorkspace() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return "runtime-workspace" // 无缓存目录的极端环境兜底
	}
	return filepath.Join(base, "algorithm-platform", "runtime-workspace")
}

type relCacheEntry struct {
	status   string
	notFound bool
	expires  time.Time
}

// Manager 算法运行时编排器。
type Manager struct {
	opts Options

	mu       sync.RWMutex
	registry map[string]*AlgoInstance

	portSeqMu sync.Mutex
	portSeq   int

	relCacheMu sync.Mutex
	relCache   map[string]*relCacheEntry
}

// New 构造编排器（按 Options 填空默认值）。
func New(opts Options) *Manager {
	if opts.DaprBin == "" {
		opts.DaprBin = "dapr"
	}
	if opts.DefaultAlgo == "" {
		opts.DefaultAlgo = "surveyRD"
	}
	if opts.ReleaseCacheTTL <= 0 {
		opts.ReleaseCacheTTL = 30 * time.Second
	}
	if opts.Workspace == "" {
		opts.Workspace = DefaultWorkspace()
	}
	return &Manager{
		opts:     opts,
		registry: map[string]*AlgoInstance{},
		relCache: map[string]*relCacheEntry{},
	}
}

// Start 启动编排器：按「最新版本=released」的算法登记并拉起。
// 算法文件来自 MinIO（已发布版本），而非固定目录；其余算法留待「发布」动作触发拉起（见 Launch）；
// 归档且无已发布版本时回收（见 MaybeKill）。
func (m *Manager) Start() {
	if m.opts.Resolver == nil {
		log.Printf("[runtime] 未配置 VersionResolver，跳过启动拉起")
		return
	}
	ctx := context.Background()
	codes, err := m.opts.Resolver.ListReleasedCodes(ctx)
	if err != nil {
		log.Printf("[runtime] 列举已发布算法失败: %v", err)
		return
	}
	log.Printf("[runtime] 启动时拉起已发布算法: %d 个（文件从 MinIO 拉取）", len(codes))
	for _, code := range codes {
		inst, err := m.registerCode(ctx, code)
		if err != nil {
			log.Printf("[runtime] 登记算法失败，跳过拉起: %s %v", code, err)
			continue
		}
		m.launchGuarded(inst)
	}
}

// Stop 优雅退出：清理所有算法子进程（dapr + 算法本体）并清理暂存。
func (m *Manager) Stop() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inst := range m.registry {
		killTree(inst.Cmd)
		if inst.StagingDir != "" {
			_ = os.RemoveAll(inst.StagingDir)
		}
	}
}

// registerCode 登记（或取回）一个算法 code 的占位实例：分配 Dapr 内部端口，并从平台取监听端口，
// manifest/dir 留待拉起时从 MinIO 绑定。平台未指定端口（旧数据）时回退到 manifest.port（见 ensureStaged）。
func (m *Manager) registerCode(ctx context.Context, code string) (*AlgoInstance, error) {
	m.mu.Lock()
	if inst, ok := m.registry[code]; ok {
		m.mu.Unlock()
		return inst, nil
	}
	inst := &AlgoInstance{Code: code}
	m.assignPorts(inst) // Dapr sidecar 的 http/grpc 内部端口（平台内部，非用户指定）

	// 算法自身监听端口：由平台在拉起时按「本机空闲端口」动态分配。Dapr 按 app-id 路由，
	// 该端口仅是算法进程与其 sidecar 之间的 localhost 内部端口，无需用户指定、也无需全局唯一。
	// 若平台已记录上次分配的端口且当前仍空闲，则复用以保持稳定；否则重新分配并持久化。
	if m.opts.PortResolver != nil {
		if p, err := m.opts.PortResolver.PortForCode(ctx, code); err == nil && p > 0 && isPortFree(p) {
			inst.AppPort = p
		} else if fp := findFreePort(); fp > 0 {
			inst.AppPort = fp
			if serr := m.opts.PortResolver.SetPort(ctx, code, fp); serr != nil {
				log.Printf("[runtime] 持久化算法 %s 端口 %d 失败: %v", code, fp, serr)
			}
		}
	}
	if inst.AppPort <= 0 {
		// 极端兜底：PortResolver 缺失或分配失败，临时分配（重启后不持久化）
		inst.AppPort = findFreePort()
	}
	m.registry[code] = inst
	m.mu.Unlock()
	log.Printf("[runtime] 登记算法: %s（app-port=%d 动态分配；dapr 端口 http=%d grpc=%d）", code, inst.AppPort, inst.DaprHTTPPort, inst.DaprGRPCPort)
	return inst, nil
}

// assignPorts 基于注册顺序确定性分配 Dapr sidecar 的 http/grpc 端口（仅供平台内部转发使用，
// 非用户指定）。算法自身监听端口（app-port）不再由用户指定，改由平台在 registerCode 时
// 按本机空闲端口动态分配（见 registerCode）。
func (m *Manager) assignPorts(inst *AlgoInstance) {
	m.portSeqMu.Lock()
	idx := m.portSeq
	m.portSeq++
	m.portSeqMu.Unlock()
	inst.DaprHTTPPort = 3500 + idx*10
	inst.DaprGRPCPort = 50001 + idx
}

// isPortFree 检测指定端口当前是否可被绑定（用于发布前预检用户声明的算法端口是否被占用）。
func isPortFree(port int) bool {
	if port <= 0 {
		return false
	}
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// findFreePort 在本机分配一个当前空闲的 TCP 端口（供算法进程自身监听）。
// 通过绑定 :0 让操作系统挑选，再立即释放；调用方应尽快占用（startInstance 会立即绑定），
// 并辅以 isPortFree 复核，降低竞态概率。
func findFreePort() int {
	for i := 0; i < 10; i++ {
		addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			continue
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		if isPortFree(port) {
			return port
		}
	}
	return 0
}

// launch 启动单个算法实例并等待就绪（先从 MinIO 拉取已发布版本文件）。
// 同步执行 ensureStaged → startInstance → waitHealthy，全程成功返回 nil；
// 任一环节失败（缺运行包 / 端口冲突 / 进程崩溃 / 健康检查超时）返回 error。
func (m *Manager) launch(inst *AlgoInstance) error {
	if err := m.ensureStaged(inst); err != nil {
		return fmt.Errorf("准备算法 %s 失败（未拉起）: %w", inst.Code, err)
	}
	// 预检：用户声明的算法端口是否被占用（避免等 60s 健康检查超时才发现，给出明确错误）
	if !isPortFree(inst.AppPort) {
		return fmt.Errorf("算法 %s 声明的端口 %d 已被占用（可能被其他算法或进程占用），请在该算法的 algorithm.yaml 中更换 port 后重新发布", inst.Code, inst.AppPort)
	}
	if err := m.startInstance(inst); err != nil {
		return fmt.Errorf("启动算法 %s 失败: %w", inst.Code, err)
	}
	// 监听进程退出，便于快速失败
	go func() {
		_ = inst.Cmd.Wait()
		inst.Healthy.Store(false)
		log.Printf("[runtime] 算法进程已退出: %s", inst.Code)
	}()
	if !m.waitHealthy(inst) {
		return fmt.Errorf("算法 %s 健康检查超时或进程提前退出（请检查 entry 是否正确、port 是否被占用、进程日志是否报错）", inst.Code)
	}
	log.Printf("[runtime] 算法就绪: %s (version:%s app:%d sidecar-http:%d grpc:%d)",
		inst.Code, inst.Version, inst.AppPort, inst.DaprHTTPPort, inst.DaprGRPCPort)
	return nil
}

// ensureStaged 确保实例已绑定 Manifest 与本地暂存目录：
// 解析最新已发布版本 → 从 MinIO 下载其全部文件到 workspace/<code>/<version>/ → 读取 algorithm.yaml。
// 已绑定则直接返回（幂等）。
func (m *Manager) ensureStaged(inst *AlgoInstance) error {
	if m.opts.Resolver == nil {
		return fmt.Errorf("未配置 VersionResolver，无法从 MinIO 拉取算法文件")
	}
	if m.opts.Downloader == nil {
		return fmt.Errorf("未配置 BlobDownloader，无法从 MinIO 下载算法文件")
	}
	ctx := context.Background()
	versionID, ver, status, err := m.opts.Resolver.LatestReleasedVersion(ctx, inst.Code)
	if err != nil {
		return fmt.Errorf("解析已发布版本失败: %w", err)
	}
	if status != statusReleased || versionID == "" {
		return fmt.Errorf("无已发布版本（最新状态=%s），无法拉起", status)
	}
	staging := filepath.Join(m.opts.Workspace, inst.Code, ver)

	// 同进程内幂等：已绑定且版本未变更，直接返回
	if inst.Manifest != nil && inst.VersionID == versionID && inst.Dir != "" && dirExists(inst.StagingDir) {
		return nil
	}

	// B: 跨重启复用——若本机已暂存该 versionID 的版本，跳过下载直接解析
	var downloaded []string
	if markerMatches(staging, versionID) && dirExists(staging) {
		downloaded, err = collectRelPaths(staging)
		if err != nil {
			return fmt.Errorf("列举暂存目录失败: %w", err)
		}
		log.Printf("[runtime] 复用已暂存算法 %s@%s（%d 个文件），跳过下载", inst.Code, ver, len(downloaded))
	} else {
		if err := m.prepareStaging(staging, inst.Code); err != nil {
			return fmt.Errorf("准备暂存目录失败: %w", err)
		}
		files, ferr := m.opts.Resolver.VersionFiles(ctx, versionID)
		if ferr != nil {
			return fmt.Errorf("列举版本文件失败: %w", ferr)
		}
		if len(files) == 0 {
			return fmt.Errorf("已发布版本无任何文件")
		}
		downloaded = make([]string, 0, len(files))
		for _, f := range files {
			rc, derr := m.opts.Downloader.Download(ctx, f.Key)
			if derr != nil {
				log.Printf("[runtime] 下载文件失败 %s: %v", f.Key, derr)
				continue
			}
			rel := sanitizeRel(f.RelPath)
			dest := filepath.Join(staging, rel)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				rc.Close()
				return fmt.Errorf("创建子目录失败: %w", err)
			}
			// A: 流式落地，避免大文件整体读入内存
			out, werr := os.Create(dest)
			if werr != nil {
				rc.Close()
				return fmt.Errorf("创建文件失败 %s: %w", dest, werr)
			}
			if _, cerr := io.Copy(out, rc); cerr != nil {
				out.Close()
				rc.Close()
				return fmt.Errorf("写入 %s 失败: %w", dest, cerr)
			}
			out.Close()
			rc.Close()
			downloaded = append(downloaded, rel)
		}
		if err := writeMarker(staging, versionID); err != nil {
			return fmt.Errorf("写入暂存标记失败: %w", err)
		}
	}
	// 定位 manifest：优先版本根目录；容忍拖拽上传整包时的一层目录前缀（如 package/algorithm.yaml）
	manifestRel, merr := locateManifest(downloaded)
	if merr != nil {
		return merr
	}
	if manifestRel == "" {
		return fmt.Errorf("已发布版本缺少 algorithm.yaml（无法解析启动契约）")
	}
	data, rerr := os.ReadFile(filepath.Join(staging, manifestRel))
	if rerr != nil {
		return fmt.Errorf("读取 algorithm.yaml 失败: %w", rerr)
	}
	mfst, perr := ParseManifest(data)
	if perr != nil {
		return fmt.Errorf("解析 algorithm.yaml 失败: %w", perr)
	}
	// app-id / 路由键以平台 code 为准，避免 manifest 内 code 与平台不一致
	mfst.Code = inst.Code
	inst.Manifest = mfst
	inst.Version = ver
	inst.VersionID = versionID
	// 进程工作目录 = manifest 所在目录：manifest 带目录前缀时（如 package/algorithm.yaml），
	// entry 里的相对路径（dotnet SvyCalc.dll）以该目录为基准解析
	inst.Dir = filepath.Join(staging, filepath.FromSlash(filepath.Dir(manifestRel)))
	inst.StagingDir = staging
	// 算法自身监听端口：由 registerCode 按本机空闲端口动态分配（已写入 inst.AppPort）。
	// 兜底：极少数情况下未分配（如 PortResolver 缺失），此处再分配一次并持久化。
	finalPort := inst.AppPort
	if finalPort <= 0 {
		finalPort = findFreePort()
		if finalPort <= 0 {
			return fmt.Errorf("算法 %s 无法分配空闲监听端口（请检查本机端口资源是否耗尽）", inst.Code)
		}
		if m.opts.PortResolver != nil {
			if serr := m.opts.PortResolver.SetPort(ctx, inst.Code, finalPort); serr != nil {
				log.Printf("[runtime] 持久化算法 %s 端口 %d 失败: %v", inst.Code, finalPort, serr)
			}
		}
		log.Printf("[runtime] 算法 %s 在 ensureStaged 阶段动态分配端口=%d", inst.Code, finalPort)
	}
	// manifest.port 仅作遗留兼容：若 manifest 仍声明了 port，以平台动态分配端口为准
	if mfst.Port > 0 && mfst.Port != finalPort {
		log.Printf("[runtime] 警告：manifest.port=%d 与平台动态分配端口=%d 不一致，运行时以平台端口为准", mfst.Port, finalPort)
	}
	inst.AppPort = finalPort
	// 依赖安装：声明了第三方依赖（Python 等）的算法在此建 venv 并安装；
	// 无依赖（含 C# 等无 dependencies 声明的算法）直接跳过，零影响。
	if err := m.installDeps(inst); err != nil {
		return fmt.Errorf("算法 %s 依赖安装失败: %w", inst.Code, err)
	}
	log.Printf("[runtime] 已从 MinIO 暂存算法 %s@%s 到 %s（%d 个文件，manifest=%s，app-port=%d）", inst.Code, ver, staging, len(downloaded), manifestRel, inst.AppPort)
	return nil
}

// installDeps 在拉起前为声明了依赖的算法准备隔离运行环境（仅 Python 等需要）：
// 检测到 algorithm.yaml 的 dependencies 或版本根 requirements.txt 时，于暂存目录建 venv 并安装依赖，
// 再把 entry 的 python 前缀改写为 venv 内解释器，从而用隔离环境运行算法。
// 无依赖的算法直接返回，绝不触碰运行环境（C# / 纯标准库 Python 均不受影响）。
func (m *Manager) installDeps(inst *AlgoInstance) error {
	if inst.Manifest == nil || inst.Dir == "" {
		return nil
	}
	// 1) 收集依赖：yaml 声明的 dependencies 与版本根 requirements.txt 取并集
	deps := append([]string{}, inst.Manifest.Dependencies...)
	if data, err := os.ReadFile(filepath.Join(inst.Dir, "requirements.txt")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if i := strings.Index(line, "#"); i >= 0 { // 去掉行内注释
				line = strings.TrimSpace(line[:i])
			}
			if line != "" {
				deps = append(deps, line)
			}
		}
	}
	if len(deps) == 0 {
		log.Printf("[runtime] 算法 %s 无第三方依赖声明，跳过 venv 安装", inst.Code)
		return nil
	}
	// 去重
	seen := map[string]bool{}
	uniq := deps[:0]
	for _, d := range deps {
		if !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	deps = uniq

	venvPy := venvPythonRel()
	venvDir := filepath.Join(inst.Dir, ".venv")

	// 2) 创建 venv（已存在则复用缓存，避免每次重装）
	if _, err := os.Stat(filepath.Join(venvDir, "pyvenv.cfg")); err != nil {
		log.Printf("[runtime] 算法 %s 创建 venv: %s", inst.Code, venvDir)
		if out, err := runCmd(inst.Dir, pythonBin(), "-m", "venv", venvPythonDirName()); err != nil {
			return fmt.Errorf("创建 venv 失败: %w\n%s", err, out)
		}
	}

	// 3) 安装依赖：优先离线 wheelhouse（./wheels 目录），否则走国内镜像联网安装
	var installEnv []string
	pipArgs := []string{"-m", "pip", "install"}
	if dirExists(filepath.Join(inst.Dir, "wheels")) {
		pipArgs = append(pipArgs, "--no-index", "--find-links", "./wheels")
	} else {
		installEnv = append(installEnv, "PIP_INDEX_URL="+pipIndexURL())
	}
	pipArgs = append(pipArgs, deps...)
	log.Printf("[runtime] 算法 %s 安装依赖: %v", inst.Code, deps)
	if out, err := runCmdEnv(inst.Dir, venvPy, installEnv, pipArgs...); err != nil {
		return fmt.Errorf("安装依赖失败: %w\n%s", err, out)
	}

	// 4) 把 entry 的 python 前缀改写为 venv 解释器（相对 inst.Dir，天然不含空格，避免 dapr run 按空格切参出错）
	inst.Manifest.Entry = rewriteEntryPython(inst.Manifest.Entry, venvPy)
	log.Printf("[runtime] 算法 %s entry 已指向 venv 解释器: %s", inst.Code, inst.Manifest.Entry)
	return nil
}

// venvPythonRel 返回 venv 内解释器的相对路径（相对算法运行目录 inst.Dir），
// 用相对路径可规避绝对路径含空格导致 dapr run 按空格切参的问题。
func venvPythonRel() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvPythonDirName(), "Scripts", "python.exe")
	}
	return filepath.Join(venvPythonDirName(), "bin", "python")
}

// venvPythonDirName 返回 venv 目录名（跨平台统一为 .venv）。
func venvPythonDirName() string { return ".venv" }

// pythonBin 返回用于创建 venv 的系统 Python 解释器（可用 PYTHON_BIN 环境变量覆盖，默认 python）。
func pythonBin() string {
	if v := os.Getenv("PYTHON_BIN"); v != "" {
		return v
	}
	return "python"
}

// pipIndexURL 返回 pip 安装所用的镜像源（可用 PIP_INDEX_URL 环境变量覆盖，默认清华镜像）。
func pipIndexURL() string {
	if v := os.Getenv("PIP_INDEX_URL"); v != "" {
		return v
	}
	return "https://pypi.tuna.tsinghua.edu.cn/simple"
}

// rewriteEntryPython 把 entry 的首个解释器 token（若形如 python/python3/xxxpython）改写为 venv 解释器；
// 若首个 token 不是 python 类（如 dotnet），则在前面插入 venv 解释器作为前缀。
func rewriteEntryPython(entry, venvPy string) string {
	fields := strings.Fields(entry)
	if len(fields) == 0 {
		return entry
	}
	if strings.Contains(strings.ToLower(fields[0]), "python") {
		fields[0] = venvPy
	} else {
		fields = append([]string{venvPy}, fields...)
	}
	return strings.Join(fields, " ")
}

// dirExists 判断目录是否存在。
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// markerFile 返回暂存目录内的版本标记文件路径（记录已暂存版本的 versionID，供跨重启复用判断）。
func markerFile(staging string) string {
	return filepath.Join(staging, ".staged")
}

// writeMarker 写入版本标记，标记该暂存目录已由指定 versionID 完整构建。
func writeMarker(staging, versionID string) error {
	return os.WriteFile(markerFile(staging), []byte(versionID), 0o644)
}

// markerMatches 判断磁盘暂存是否为指定 versionID 所建（标记缺失或不符均视为需重建）。
func markerMatches(staging, versionID string) bool {
	data, err := os.ReadFile(markerFile(staging))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == versionID
}

// collectRelPaths 遍历目录返回相对路径（slash 形式），用于复用缓存时定位 manifest。
func collectRelPaths(root string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	return rels, err
}

// runCmd 在指定工作目录执行命令，返回合并的标准输出与错误输出。
func runCmd(dir, exe string, args ...string) (string, error) {
	return runCmdEnv(dir, exe, nil, args...)
}

// runCmdEnv 在指定工作目录执行命令，可附加额外环境变量。
// 注意：以系统完整环境（os.Environ，保留代理）为基底，便于 pip 联网安装第三方依赖；
// 与 startInstance 不同（后者用 filteredEnv 去掉代理，仅 localhost 转发无需出网）。
func runCmdEnv(dir, exe string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Env, extraEnv...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// locateManifest 在已下载文件中定位 algorithm.yaml：
// 优先版本根目录的 algorithm.yaml；否则取路径层级最浅的那个（拖拽上传整个文件夹时
// 文件会带目录前缀，如 package/algorithm.yaml，此时以该子目录为运行目录）。
// 同一深度出现多个同名 manifest 时视为歧义，报错拒绝。
func locateManifest(rels []string) (string, error) {
	best := ""
	bestDepth := -1
	count := 0
	for _, rel := range rels {
		p := strings.ToLower(filepath.ToSlash(rel))
		if p != "algorithm.yaml" && !strings.HasSuffix(p, "/algorithm.yaml") {
			continue
		}
		depth := strings.Count(strings.TrimSuffix(p, "algorithm.yaml"), "/")
		switch {
		case best == "" || depth < bestDepth:
			best, bestDepth, count = rel, depth, 1
		case depth == bestDepth:
			count++
		}
	}
	if count > 1 {
		return "", fmt.Errorf("版本内存在多个同一层级的 algorithm.yaml，无法确定启动契约")
	}
	return best, nil
}

// prepareStaging 准备暂存目录：若目录已存在，先尝试清空；若因残留进程占用文件而清空失败，
// 则在 Windows 上清理上一轮后端遗留的算法进程（daprd/dotnet/dapr），再重试，避免「文件被另一进程占用」导致拉起失败。
// dev 场景下常因直接杀掉后端主进程、Dapr 子进程树未退出而触发此清理。
func (m *Manager) prepareStaging(staging, code string) error {
	if _, err := os.Stat(staging); os.IsNotExist(err) {
		return os.MkdirAll(staging, 0o755)
	}
	if err := os.RemoveAll(staging); err == nil {
		return os.MkdirAll(staging, 0o755)
	}
	log.Printf("[runtime] 暂存目录 %s 删除失败（可能被旧进程占用），尝试按 app-id 清理孤儿进程", staging)
	if runtime.GOOS == "windows" {
		killOrphanProcesses(code, false)
		time.Sleep(500 * time.Millisecond) // 等待进程退出释放文件锁
		if err := os.RemoveAll(staging); err == nil {
			return os.MkdirAll(staging, 0o755)
		}
		// 最后手段：清理所有遗留的 daprd/dotnet（dev 环境下这些仅由本后端拉起，安全）
		log.Printf("[runtime] 仍无法清理 %s，最后手段：清理全部遗留 daprd/dotnet 进程", staging)
		killOrphanProcesses(code, true)
		time.Sleep(800 * time.Millisecond)
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("清理暂存目录失败（可能有进程占用 %s）: %w", staging, err)
	}
	return os.MkdirAll(staging, 0o755)
}

// killOrphanProcesses 在 Windows 上清理遗留的算法进程。
// broad=false 时仅按命令行含 "--app-id <code>" 精确匹配（dapr 边车/CLI）；
// broad=true 时（最后手段）直接清理全部 daprd.exe / dotnet.exe（dev 环境这些仅由本后端拉起）。
func killOrphanProcesses(appID string, broad bool) {
	var ps string
	if broad {
		ps = "Get-CimInstance Win32_Process -Filter \"Name='daprd.exe' OR Name='dotnet.exe'\" | " +
			"ForEach-Object { Stop-Process -Id $_.ProcessId -Force }"
	} else {
		ps = fmt.Sprintf(
			"Get-CimInstance Win32_Process -Filter \"Name='dapr.exe' OR Name='daprd.exe' OR Name='dotnet.exe'\" | "+
				"Where-Object { $_.CommandLine -like '*-app-id %s*' } | "+
				"ForEach-Object { Stop-Process -Id $_.ProcessId -Force }",
			appID,
		)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", ps)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		log.Printf("[runtime] 清理孤儿进程失败（broad=%v, app-id=%s）: %v", broad, appID, err)
	}
}

// sanitizeRel 把对象存储的相对路径清洗为安全、无越级的本地相对路径。
func sanitizeRel(p string) string {
	p = filepath.ToSlash(p)
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			continue
		}
		out = append(out, s)
	}
	return strings.Join(out, "/")
}

// launchGuarded 带重入保护的拉起：已在运行或正在拉起则 no-op，避免重复拉起。
func (m *Manager) launchGuarded(inst *AlgoInstance) {
	if inst.Healthy.Load() {
		return
	}
	if !inst.launching.CompareAndSwap(false, true) {
		return // 正在拉起，忽略重复请求
	}
	defer inst.launching.Store(false)
	go func() {
		if err := m.launch(inst); err != nil {
			log.Printf("[runtime] 后台拉起算法 %s 失败: %v", inst.Code, err)
		}
	}()
}

// Launch 供「发布」动作触发：登记并同步拉起指定 code 的算法进程，阻塞直到就绪或超时。
// 拉起成功返回 nil；拉起失败（缺运行包 / 端口冲突 / 进程崩溃 / 健康检查超时等）返回 error，
// 供发布环节 Fail-Fast —— 拉不起来就应当作「发布失败」而非静默成功。
// 若已登记且健康则直接返回 nil；若正在拉起中，则等待其结束后再判定（避免并发重复拉起）。
func (m *Manager) Launch(code string) error {
	inst, err := m.registerCode(context.Background(), code)
	if err != nil {
		return fmt.Errorf("登记算法 %s 失败: %w", code, err)
	}
	if inst.Healthy.Load() {
		return nil
	}
	// 等待可能正在进行的拉起，最长 90s（避免并发重复拉起，也覆盖 waitHealthy 的耗时）
	deadline := time.Now().Add(90 * time.Second)
	for inst.launching.Load() {
		if time.Now().After(deadline) {
			return fmt.Errorf("算法 %s 拉起超时（另一拉起仍在进行）", code)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if inst.Healthy.Load() {
		return nil
	}
	if !inst.launching.CompareAndSwap(false, true) {
		// 极短竞态：被其他调用抢到，递归重试等待其完成
		return m.Launch(code)
	}
	defer inst.launching.Store(false)
	return m.launch(inst)
}

// MaybeKill 供「归档」动作触发：仅当该算法最新版本已无 released 时回收其进程，
// 避免发布新版本时自动归档旧版本导致误杀仍在线算法。
func (m *Manager) MaybeKill(code string) {
	inst, ok := m.GetInstance(code)
	if !ok || inst.Cmd == nil {
		return // 未登记或未运行
	}
	if m.isReleased(code) {
		log.Printf("[runtime] 算法 %s 仍有已发布版本，保留进程", code)
		return
	}
	log.Printf("[runtime] 算法 %s 已无已发布版本，归档回收进程", code)
	m.kill(inst)
}

// kill 杀掉单个实例的进程树并重置状态，并清理其本地暂存目录（调用方需已取到实例）。
func (m *Manager) kill(inst *AlgoInstance) {
	if inst.Cmd != nil {
		killTree(inst.Cmd)
	}
	inst.Healthy.Store(false)
	inst.Cmd = nil
	if inst.StagingDir != "" {
		if err := os.RemoveAll(inst.StagingDir); err != nil {
			log.Printf("[runtime] 清理暂存目录失败 %s: %v", inst.StagingDir, err)
		} else {
			log.Printf("[runtime] 已清理暂存目录: %s", inst.StagingDir)
		}
		inst.StagingDir = ""
	}
	inst.Dir = ""
	inst.Manifest = nil
	inst.Version = ""
	inst.VersionID = ""
}

// isReleased 查询该算法最新版本状态是否为 released（依赖 StatusQuerier）。
func (m *Manager) isReleased(code string) bool {
	if m.opts.StatusQuerier == nil {
		return false
	}
	st, err := m.opts.StatusQuerier.LatestVersionStatus(context.Background(), code)
	if err != nil {
		return false
	}
	return st == statusReleased
}

func (m *Manager) startInstance(inst *AlgoInstance) error {
	parts := strings.Fields(inst.Manifest.Entry)
	if len(parts) == 0 {
		return fmt.Errorf("manifest entry 为空")
	}
	args := []string{
		"run",
		"--app-id", inst.Manifest.Code,
		"--app-port", strconv.Itoa(inst.AppPort),
		"--dapr-http-port", strconv.Itoa(inst.DaprHTTPPort),
		"--dapr-grpc-port", strconv.Itoa(inst.DaprGRPCPort),
		// 我们只用 service invocation（HTTP 转发），不使用 actor，因此禁用 placement 与 scheduler，
		// 避免 Dapr 反复重试连接这两个未启动的服务而刷屏。
		"--placement-host-address", "",
		"--scheduler-host-address", "",
		"--",
	}
	args = append(args, parts...)
	cmd := exec.Command(m.opts.DaprBin, args...)
	cmd.Dir = inst.Dir
	// 清掉代理变量（避免 dapr/dotnet 走代理 & 撞键问题），并注入 APP_PORT 让算法端口与 manifest 一致
	cmd.Env = append(filteredEnv(), "APP_PORT="+strconv.Itoa(inst.AppPort))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	inst.Cmd = cmd
	return nil
}

// killTree 整树回收（Windows 用 taskkill /T 杀 dapr run + daprd + 算法；Unix 退化为 Process.Kill）。
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
	}
	_ = cmd.Process.Kill()
}

// filteredEnv 返回去掉代理变量后的环境（避免 dapr/dotnet 走代理）。
func filteredEnv() []string {
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		lk := strings.ToLower(k)
		if lk == "http_proxy" || lk == "https_proxy" || lk == "all_proxy" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// waitHealthy 阻塞等待算法健康；最多等 60s。
func (m *Manager) waitHealthy(inst *AlgoInstance) bool {
	url := fmt.Sprintf("http://localhost:%d%s", inst.AppPort, inst.Manifest.Health)
	for i := 0; i < 60; i++ {
		resp, err := http.Get(url)
		if err == nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			if code == 200 {
				inst.Healthy.Store(true)
				return true
			}
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

// Invoke 经 Dapr sidecar 的 HTTP 调用算法，返回原始响应体（调用方负责解释）。
// headers 为透传给算法进程的可选额外请求头（如会话 X-Session-Id）；传 nil 表示无附加头。
func (m *Manager) Invoke(inst *AlgoInstance, daprMethod, httpMethod string, body []byte, headers http.Header) ([]byte, int, error) {
	code := ""
	if inst != nil {
		if inst.Manifest != nil {
			code = inst.Manifest.Code
		} else {
			code = inst.Code
		}
	}
	// 拉起失败的实例 Manifest 为空（如已发布版本缺 algorithm.yaml），不可解引用，返回干净 503 而非 panic。
	if inst == nil || inst.Manifest == nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("算法 %s 尚未就绪（Manifest 为空，可能拉起失败；请确认已发布版本包含 algorithm.yaml 等运行包文件）", code)
	}
	if !inst.Healthy.Load() {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("算法尚未就绪: %s", inst.Manifest.Code)
	}
	target := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", inst.DaprHTTPPort, inst.Manifest.Code, daprMethod)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(httpMethod, target, reader)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	req.Header.Set("Content-Type", "application/json")
	if headers != nil {
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Set(k, v)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

// ReleaseCheck 发布状态闸门：返回 (是否允许调用, HTTP 状态码, 拒绝原因)。
// 规则「算法最新版本状态不为已发布，则无法调用」。未注册 / 查询失败 / 非 released 一律拒绝（fail-closed）。
func (m *Manager) ReleaseCheck(code string) (ok bool, httpStatus int, msg string) {
	if !m.opts.ReleaseCheckEnabled {
		return true, 0, ""
	}
	// 缓存命中（含 notFound）
	m.relCacheMu.Lock()
	if e, hit := m.relCache[code]; hit && time.Now().Before(e.expires) {
		if e.notFound {
			m.relCacheMu.Unlock()
			return false, http.StatusNotFound, fmt.Sprintf("算法 %s 未在平台注册（无法确认已发布），拒绝调用", code)
		}
		if e.status == statusReleased {
			m.relCacheMu.Unlock()
			return true, 0, ""
		}
		m.relCacheMu.Unlock()
		return false, http.StatusForbidden, fmt.Sprintf("算法 %s 最新版本状态为 %s（非已发布），拒绝调用", code, e.status)
	}
	m.relCacheMu.Unlock()

	if m.opts.StatusQuerier == nil {
		return false, http.StatusServiceUnavailable, "发布校验未配置（无状态查询器），拒绝调用"
	}
	status, err := m.opts.StatusQuerier.LatestVersionStatus(context.Background(), code)
	if err != nil {
		m.relCacheMu.Lock()
		m.relCache[code] = &relCacheEntry{notFound: true, expires: time.Now().Add(m.opts.ReleaseCacheTTL)}
		m.relCacheMu.Unlock()
		return false, http.StatusNotFound, fmt.Sprintf("算法 %s 未在平台注册（无法确认已发布），拒绝调用", code)
	}
	m.relCacheMu.Lock()
	m.relCache[code] = &relCacheEntry{status: status, expires: time.Now().Add(m.opts.ReleaseCacheTTL)}
	m.relCacheMu.Unlock()
	if status != statusReleased {
		return false, http.StatusForbidden, fmt.Sprintf("算法 %s 最新版本状态为 %s（非已发布），拒绝调用", code, status)
	}
	return true, 0, ""
}

// GetInstance 按 code 取运行实例。
func (m *Manager) GetInstance(code string) (*AlgoInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.registry[code]
	return inst, ok
}
