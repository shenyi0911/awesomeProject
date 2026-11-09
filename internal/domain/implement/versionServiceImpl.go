package implement

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/service"
	"awesomeProject/internal/filestorage"
	repoiface "awesomeProject/internal/repository/service"
	"awesomeProject/internal/runtime"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/utils"
)

type versionService struct {
	algoRepo repoiface.AlgorithmRepository
	verRepo  repoiface.VersionRepository
	storage  filestorage.FileStorage
}

func NewVersionService(algoRepo repoiface.AlgorithmRepository, verRepo repoiface.VersionRepository, storage filestorage.FileStorage) service.VersionService {
	return &versionService{
		algoRepo: algoRepo,
		verRepo:  verRepo,
		storage:  storage,
	}
}

var versionNumRe = regexp.MustCompile(`(\d+)`)

func (s *versionService) Create(req request.CreateVersionRequest) (*entity.AlgorithmVersion, error) {
	req.AlgorithmID = strings.TrimSpace(req.AlgorithmID)
	if _, err := s.algoRepo.GetByID(req.AlgorithmID); err != nil {
		return nil, errors.New("algorithm not found: " + req.AlgorithmID)
	}

	// 约束：历史版本必须全部为已归档状态，否则不允许新建
	if s.hasNonArchivedVersion(req.AlgorithmID) {
		return nil, errors.New("please archive all existing versions before creating a new version")
	}

	ver := strings.TrimSpace(req.Version)
	if ver == "" {
		ver = s.nextVersionNumber(req.AlgorithmID)
	}
	if !versionNumRe.MatchString(ver) {
		return nil, errors.New("version must contain a number, e.g. v1, v1.0.0")
	}

	entity := &entity.AlgorithmVersion{
		ID:            utils.NewID("ver"),
		AlgorithmID:   req.AlgorithmID,
		Version:       ver,
		Description:   strings.TrimSpace(req.Description),
		Status:        enum.VersionStatusDraft,
		IsActive:      false,
		EntryPoint:    strings.TrimSpace(req.EntryPoint),
		ParameterSpec: strings.TrimSpace(req.ParameterSpec),
		ReleaseNote:   strings.TrimSpace(req.ReleaseNote),
		Files:         []entity.AlgorithmFile{},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := s.verRepo.Create(entity); err != nil {
		logger.Error("create version failed", "error", err)
		return nil, err
	}

	s.logChange(entity, normalizeOperator(req.Operator), enum.ChangeActionCreate, "创建版本", "")
	return entity, nil
}

// hasNonArchivedVersion 判断算法下是否存在非归档（草稿/已发布）版本
func (s *versionService) hasNonArchivedVersion(algorithmID string) bool {
	versions, _ := s.verRepo.ListByAlgorithm(algorithmID, request.VersionFilter{Limit: 10000, Offset: 0})
	for _, v := range versions {
		if v.Status != enum.VersionStatusArchived {
			return true
		}
	}
	return false
}

// archiveNonArchivedVersions 归档同一算法下所有非归档版本（草稿/已发布），excludeID 版本除外
func (s *versionService) archiveNonArchivedVersions(algorithmID, excludeID, operator string) error {
	versions, _ := s.verRepo.ListByAlgorithm(algorithmID, request.VersionFilter{Limit: 10000, Offset: 0})
	for _, v := range versions {
		if v.ID == excludeID {
			continue
		}
		if v.Status != enum.VersionStatusArchived {
			v.Status = enum.VersionStatusArchived
			v.IsActive = false
			v.UpdatedAt = time.Now()
			if err := s.verRepo.Update(&v); err != nil {
				return err
			}
			s.logChange(&v, normalizeOperator(operator), enum.ChangeActionArchive, "新建/发布版本时自动归档旧版本", "")
		}
	}
	return nil
}

// nextVersionNumber 按算法维度自增生成版本号（取已有版本主版本号最大值 +1）
func (s *versionService) nextVersionNumber(algorithmID string) string {
	versions, _ := s.verRepo.ListByAlgorithm(algorithmID, request.VersionFilter{Limit: 10000, Offset: 0})
	maxN := 0
	for _, v := range versions {
		n := parseMajorVersion(v.Version)
		if n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("v%d", maxN+1)
}

// parseMajorVersion 解析主版本号整数，如 v1.0.0 -> 1，无效返回 0
func parseMajorVersion(v string) int {
	parts := strings.Split(v, ".")
	if len(parts) == 0 {
		return 0
	}
	nums := versionNumRe.FindStringSubmatch(parts[0])
	if len(nums) < 2 {
		return 0
	}
	n, err := strconv.Atoi(nums[1])
	if err != nil {
		return 0
	}
	return n
}

func (s *versionService) Update(id string, req request.UpdateVersionRequest) (*entity.AlgorithmVersion, error) {
	v, err := s.verRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	// 约束：仅草稿可编辑；已发布/归档版本不可修改，需新建版本迭代
	if v.Status != enum.VersionStatusDraft {
		return nil, errors.New("only draft version can be edited; archived/published versions are immutable")
	}

	if req.Description != "" {
		v.Description = strings.TrimSpace(req.Description)
	}
	if req.EntryPoint != "" {
		v.EntryPoint = strings.TrimSpace(req.EntryPoint)
	}
	if req.ParameterSpec != "" {
		v.ParameterSpec = strings.TrimSpace(req.ParameterSpec)
	}
	if req.ReleaseNote != "" {
		v.ReleaseNote = strings.TrimSpace(req.ReleaseNote)
	}

	if err := s.verRepo.Update(v); err != nil {
		logger.Error("update version failed", "error", err)
		return nil, err
	}
	s.logChange(v, normalizeOperator(req.Operator), enum.ChangeActionEdit, "编辑版本元信息", "")
	return v, nil
}

func (s *versionService) Delete(id string) error {
	// 级联删除该版本的变更记录，再删版本记录
	if err := s.verRepo.DeleteChangeLogs(id); err != nil {
		logger.Warn("delete version change logs failed", "id", id, "error", err)
	}
	return s.verRepo.Delete(id)
}

func (s *versionService) GetByID(id string) (*entity.AlgorithmVersion, error) {
	return s.verRepo.GetByID(id)
}

// AlgorithmCode 由算法 ID 取算法 code（运行时据此定位算法包与 Dapr app-id）。
func (s *versionService) AlgorithmCode(algoID string) (string, error) {
	algo, err := s.algoRepo.GetByID(algoID)
	if err != nil {
		return "", err
	}
	return algo.Code, nil
}

func (s *versionService) List(req request.ListVersionRequest) ([]entity.AlgorithmVersion, int) {
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 200 {
		req.Limit = 200
	}
	return s.verRepo.ListByAlgorithm(req.AlgorithmID, request.VersionFilter{
		Version: req.Version,
		Status:  req.Status,
		Limit:   req.Limit,
		Offset:  req.Offset,
	})
}

func (s *versionService) GetActive(algorithmID string) (*entity.AlgorithmVersion, error) {
	return s.verRepo.GetActiveByAlgorithm(algorithmID)
}

// Publish 发布：draft -> released（经过验证，允许业务正常调用）
// 规则：一个算法只能有一个已发布版本，发布前自动归档该算法已有的已发布版本
func (s *versionService) Publish(id string, operator string) (*entity.AlgorithmVersion, error) {
	v, err := s.verRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if v.Status == enum.VersionStatusReleased {
		return nil, errors.New("version already published")
	}
	if v.Status == enum.VersionStatusArchived {
		return nil, errors.New("archived version cannot be published")
	}

	// 发布前校验：该版本是否具备被运行时拉起所需的最小文件集（algorithm.yaml + entry 指向的主文件）
	if err := s.validateLaunchable(v); err != nil {
		return nil, err
	}

	// 先归档同一算法下所有非归档版本（草稿/已发布），保证唯一性
	if err := s.archiveNonArchivedVersions(v.AlgorithmID, v.ID, operator); err != nil {
		return nil, err
	}

	v.Status = enum.VersionStatusReleased
	v.IsActive = true
	v.UpdatedAt = time.Now()
	if err := s.verRepo.Update(v); err != nil {
		return nil, err
	}
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionPublish, "发布版本", "")
	return v, nil
}

// RevertPublish 将已发布版本回滚为草稿。
// 发布时会同步拉起算法进程；若拉起失败（缺运行包 / 端口冲突 / 进程崩溃等），应回滚发布状态，
// 避免数据库里留下「已发布但没起来」的僵尸版本——否则前端显示已发布、调用却 503，体验割裂。
func (s *versionService) RevertPublish(id string, operator string) error {
	v, err := s.verRepo.GetByID(id)
	if err != nil {
		return err
	}
	if v.Status != enum.VersionStatusReleased {
		return nil
	}
	v.Status = enum.VersionStatusDraft
	v.IsActive = false
	v.UpdatedAt = time.Now()
	if err := s.verRepo.Update(v); err != nil {
		return err
	}
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionPublish, "发布失败回滚为草稿", "")
	return nil
}

// validateLaunchable 发布前校验：版本是否具备被运行时拉起所需的最小文件集。
// 必须包含 algorithm.yaml（启动契约），且其声明的 entry 所指向的主文件（如 SvyCalc.dll / app.py）
// 已随版本上传；否则发布后运行时拉起必失败。校验在归档其他版本之前进行，避免误伤。
func (s *versionService) validateLaunchable(v *entity.AlgorithmVersion) error {
	if len(v.Files) == 0 {
		return errors.New("版本没有任何文件，无法启动算法（请先上传 algorithm.yaml 与可运行产物）")
	}
	// 定位 algorithm.yaml
	var yamlFile *entity.AlgorithmFile
	for i := range v.Files {
		if strings.EqualFold(filepath.Base(v.Files[i].Name), "algorithm.yaml") {
			yamlFile = &v.Files[i]
			break
		}
	}
	if yamlFile == nil {
		return errors.New("缺少 algorithm.yaml（启动契约），无法启动算法")
	}
	rc, derr := s.storage.Download(context.Background(), yamlFile.Path)
	if derr != nil {
		return fmt.Errorf("读取 algorithm.yaml 失败: %w", derr)
	}
	data, rerr := io.ReadAll(rc)
	rc.Close()
	if rerr != nil {
		return fmt.Errorf("读取 algorithm.yaml 失败: %w", rerr)
	}
	mfst, perr := validateManifestContent(data)
	if perr != nil {
		return perr
	}
	mainFile := entryMainFile(mfst.Entry)
	if mainFile == "" {
		return errors.New("algorithm.yaml 的 entry 无法解析出启动文件")
	}
	if !entryFilePresent(mainFile, v.Files) {
		return fmt.Errorf("entry 指向的启动文件「%s」未随版本上传，无法启动算法", mainFile)
	}
	return nil
}

// entryMainFile 从 entry 命令中取出被执行的脚本/程序文件名（如 "dotnet SvyCalc.dll" → "SvyCalc.dll"）。
func entryMainFile(entry string) string {
	fields := strings.Fields(entry)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// entryFilePresent 判断 entry 指向的文件是否存在于版本文件集中（按相对路径或文件名匹配）。
func entryFilePresent(target string, files []entity.AlgorithmFile) bool {
	base := filepath.Base(target)
	for _, f := range files {
		if f.Name == target || filepath.Base(f.Name) == base {
			return true
		}
	}
	return false
}

// validateManifestContent 解析并校验 algorithm.yaml：返回解析结果，并在不可解析或缺 entry 时报错
// （code 由平台覆盖，缺失不算错）。供「上传即时校验」与「发布校验」共用，避免重复解析逻辑。
func validateManifestContent(data []byte) (*runtime.Manifest, error) {
	mfst, err := runtime.ParseManifest(data)
	if err != nil && !errors.Is(err, runtime.ErrMissingCode) {
		return nil, fmt.Errorf("algorithm.yaml 解析失败: %w", err)
	}
	if mfst == nil || mfst.Entry == "" {
		return nil, errors.New("algorithm.yaml 缺少 entry（启动命令），无法作为启动契约")
	}
	return mfst, nil
}

// Archive 归档：released/draft -> archived（保留历史，禁止新任务调用，逻辑删除）
func (s *versionService) Archive(id string, operator string) (*entity.AlgorithmVersion, error) {
	v, err := s.verRepo.GetByID(id)
	if err != nil {
		return nil, err
	}
	if v.Status == enum.VersionStatusArchived {
		return nil, errors.New("version already archived")
	}
	v.Status = enum.VersionStatusArchived
	v.IsActive = false
	v.UpdatedAt = time.Now()
	if err := s.verRepo.Update(v); err != nil {
		return nil, err
	}
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionArchive, "归档版本", "")
	return v, nil
}

// UploadFile 上传版本文件：仅草稿可上传；独立绑定本版本，旧版本文件不受影响
func (s *versionService) UploadFile(ctx context.Context, versionID string, in request.UploadFileInput, operator string) (*entity.AlgorithmFile, error) {
	v, err := s.verRepo.GetByID(versionID)
	if err != nil {
		return nil, err
	}
	if v.Status != enum.VersionStatusDraft {
		return nil, errors.New("only draft version can upload files")
	}

	// 文件夹上传时保留相对路径（含子目录），单文件上传退化为文件名
	relPath := in.RelativePath
	if relPath == "" {
		relPath = in.Filename
	}

	key := buildVersionFileKey(versionID, relPath)

	// C: 上传即校验启动契约——若本次上传的正是 algorithm.yaml，立即解析，语法/结构错误当场报错，不必等到发布
	// A: 其余文件流式上传，边传边算 MD5，避免大文件整体读入内存
	var (
		md5sum     string
		actualSize int64
	)
	if strings.EqualFold(filepath.Base(relPath), "algorithm.yaml") {
		data, rerr := io.ReadAll(in.Reader)
		if rerr != nil {
			return nil, fmt.Errorf("读取上传流失败: %w", rerr)
		}
		if _, verr := validateManifestContent(data); verr != nil {
			return nil, verr
		}
		md5sum = fmt.Sprintf("%x", md5.Sum(data))
		actualSize = int64(len(data))
		if err := s.storage.Upload(ctx, key, bytes.NewReader(data), actualSize, in.ContentType); err != nil {
			return nil, err
		}
	} else {
		uploadSize := in.Size
		if uploadSize <= 0 {
			uploadSize = -1 // 未知大小时交由 S3 客户端流式上传
		}
		h := md5.New()
		cr := &countingReader{r: in.Reader}
		if err := s.storage.Upload(ctx, key, io.TeeReader(cr, h), uploadSize, in.ContentType); err != nil {
			return nil, err
		}
		md5sum = fmt.Sprintf("%x", h.Sum(nil))
		actualSize = cr.n
	}

	file := &entity.AlgorithmFile{
		Name:        relPath,
		Path:        key,
		Size:        actualSize,
		Type:        strings.TrimPrefix(path.Ext(relPath), "."),
		ContentType: in.ContentType,
	}
	v.Files = append(v.Files, *file)
	if err := s.verRepo.Update(v); err != nil {
		_ = s.storage.Delete(ctx, key)
		return nil, err
	}
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionUploadFile, "上传文件: "+relPath, md5sum)
	return file, nil
}

// UploadFiles 批量上传版本文件：一次请求（一次拖拽/一次文件夹选择）合并为一条变更记录。
// 任一文件失败则回滚本次已上传的对象，版本文件列表保持原状。
func (s *versionService) UploadFiles(ctx context.Context, versionID string, ins []request.UploadFileInput, operator string) ([]*entity.AlgorithmFile, error) {
	if len(ins) == 0 {
		return nil, errors.New("no files to upload")
	}
	v, err := s.verRepo.GetByID(versionID)
	if err != nil {
		return nil, err
	}
	if v.Status != enum.VersionStatusDraft {
		return nil, errors.New("only draft version can upload files")
	}

	var (
		uploaded []string                // 已成功上传的对象 key，失败时回滚删除
		files    []*entity.AlgorithmFile // 返回给前端的文件实体
		names    []string                // 变更记录说明用
	)
	for _, in := range ins {
		// 文件夹上传时保留相对路径（含子目录），单文件上传退化为文件名
		relPath := in.RelativePath
		if relPath == "" {
			relPath = in.Filename
		}
		key := buildVersionFileKey(versionID, relPath)

		// C: 批量上传中若含 algorithm.yaml，立即校验契约合法性（A: 其余文件流式上传，批量不逐文件记 MD5）
		var size int64
		if strings.EqualFold(filepath.Base(relPath), "algorithm.yaml") {
			data, rerr := io.ReadAll(in.Reader)
			if rerr != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, fmt.Errorf("读取上传流失败: %w", rerr)
			}
			if _, verr := validateManifestContent(data); verr != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, verr
			}
			if err := s.storage.Upload(ctx, key, bytes.NewReader(data), int64(len(data)), in.ContentType); err != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, err
			}
			size = int64(len(data))
		} else {
			uploadSize := in.Size
			if uploadSize <= 0 {
				uploadSize = -1
			}
			cr := &countingReader{r: in.Reader}
			if err := s.storage.Upload(ctx, key, cr, uploadSize, in.ContentType); err != nil {
				s.rollbackUploads(ctx, uploaded)
				return nil, err
			}
			size = cr.n
		}
		uploaded = append(uploaded, key)
		files = append(files, &entity.AlgorithmFile{
			Name:        relPath,
			Path:        key,
			Size:        size,
			Type:        strings.TrimPrefix(path.Ext(relPath), "."),
			ContentType: in.ContentType,
		})
		names = append(names, relPath)
	}

	for _, f := range files {
		v.Files = append(v.Files, *f)
	}
	if err := s.verRepo.Update(v); err != nil {
		s.rollbackUploads(ctx, uploaded)
		return nil, err
	}
	// 一次上传只记一条变更记录
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionUploadFile, batchUploadDesc(names), "")
	return files, nil
}

// rollbackUploads 失败回滚：删除本次已上传到对象存储的文件
func (s *versionService) rollbackUploads(ctx context.Context, keys []string) {
	for _, k := range keys {
		_ = s.storage.Delete(ctx, k)
	}
}

// batchUploadDesc 生成批量上传的合并说明：≤5 个全部列出，否则列前 3 个 + 总数
func batchUploadDesc(names []string) string {
	summary := fmt.Sprintf("（共 %d 个）", len(names))
	if len(names) <= 5 {
		return "上传文件: " + strings.Join(names, "、") + summary
	}
	return "上传文件: " + strings.Join(names[:3], "、") + " 等 " + summary
}

// DeleteFile 删除版本文件：仅草稿可删除
func (s *versionService) DeleteFile(ctx context.Context, versionID, key, operator string) error {
	v, err := s.verRepo.GetByID(versionID)
	if err != nil {
		return err
	}
	if v.Status != enum.VersionStatusDraft {
		return errors.New("only draft version can delete files")
	}
	idx := -1
	for i, f := range v.Files {
		if f.Path == key || f.Name == key {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("file not found in version: " + key)
	}
	name := v.Files[idx].Name
	actualKey := v.Files[idx].Path
	if err := s.storage.Delete(ctx, actualKey); err != nil {
		return err
	}
	v.Files = append(v.Files[:idx], v.Files[idx+1:]...)
	if err := s.verRepo.Update(v); err != nil {
		return err
	}
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionDeleteFile, "删除文件: "+name, "")
	return nil
}

// GetFileURL 生成版本文件下载地址
func (s *versionService) GetFileURL(ctx context.Context, versionID, key, operator string) (string, error) {
	v, err := s.verRepo.GetByID(versionID)
	if err != nil {
		return "", err
	}
	found := false
	actualKey := ""
	fileName := ""
	for _, f := range v.Files {
		if f.Path == key || f.Name == key {
			found = true
			actualKey = f.Path
			fileName = f.Name
			break
		}
	}
	if !found {
		return "", errors.New("file not found in version: " + key)
	}
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionDownloadFile, "下载文件: "+fileName, "")
	return s.storage.PresignURL(ctx, actualKey, 15*time.Minute)
}

// BatchDownloadFiles 将选中文件打包为 zip 返回。keys 支持完整 Path 或相对 Name 匹配；
// 找不到的文件会被跳过；zip 内条目名使用文件相对路径（保留子目录结构）。
func (s *versionService) BatchDownloadFiles(ctx context.Context, versionID string, keys []string, operator string) (io.ReadCloser, string, error) {
	v, err := s.verRepo.GetByID(versionID)
	if err != nil {
		return nil, "", err
	}
	if len(keys) == 0 {
		return nil, "", errors.New("no file selected")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]int{}
	var downloadedNames []string
	for _, key := range keys {
		actualKey := ""
		relName := ""
		for _, f := range v.Files {
			if f.Path == key || f.Name == key {
				actualKey = f.Path
				relName = f.Name
				break
			}
		}
		if actualKey == "" {
			continue // 跳过不存在的文件
		}
		downloadedNames = append(downloadedNames, relName)
		rc, derr := s.storage.Download(ctx, actualKey)
		if derr != nil {
			_ = zw.Close()
			return nil, "", fmt.Errorf("download %s failed: %w", relName, derr)
		}
		entry := relName
		if n, ok := seen[entry]; ok {
			seen[entry] = n + 1
			entry = fmt.Sprintf("%s_%d", entry, n)
		} else {
			seen[entry] = 1
		}
		w, werr := zw.Create(entry)
		if werr != nil {
			rc.Close()
			_ = zw.Close()
			return nil, "", werr
		}
		if _, cerr := io.Copy(w, rc); cerr != nil {
			rc.Close()
			_ = zw.Close()
			return nil, "", cerr
		}
		rc.Close()
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	// 记录批量下载操作日志（说明里列出文件名；超过 5 个时只显示数量，避免说明过长）
	desc := "批量下载文件: " + strings.Join(downloadedNames, "、")
	if len(downloadedNames) > 5 {
		desc = fmt.Sprintf("批量下载文件（共 %d 个）", len(downloadedNames))
	}
	s.logChange(v, normalizeOperator(operator), enum.ChangeActionDownloadFile, desc, "")
	zipName := fmt.Sprintf("version-%s-files.zip", v.Version)
	return io.NopCloser(bytes.NewReader(buf.Bytes())), zipName, nil
}

func (s *versionService) ListChangeLog(versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return s.verRepo.ListChangeLog(versionID, limit, offset)
}

// logChange 写入版本变更记录
func (s *versionService) logChange(v *entity.AlgorithmVersion, operator, action, desc, fileMD5 string) {
	log := &entity.VersionChangeLog{
		ID:          utils.NewID("log"),
		AlgorithmID: v.AlgorithmID,
		VersionID:   v.ID,
		Version:     v.Version,
		Operator:    operator,
		Action:      action,
		ChangeTime:  time.Now(),
		ChangeDesc:  desc,
		FileMD5:     fileMD5,
	}
	if err := s.verRepo.CreateChangeLog(log); err != nil {
		logger.Warn("write version change log failed", "error", err)
	}
}

func normalizeOperator(op string) string {
	op = strings.TrimSpace(op)
	if op == "" {
		return "system"
	}
	return op
}

// countingReader 包装 io.Reader 并统计已读取字节数，用于流式上传时记录真实大小。
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// buildVersionFileKey 生成版本文件对象存储 key：versions/{versionID}/{uuid}/{相对路径}
// 保留文件夹上传时的子目录层级（正斜杠），仅对反斜杠和越级路径做安全化
func buildVersionFileKey(versionID, relativePath string) string {
	// 统一分隔符为 /
	p := strings.ReplaceAll(relativePath, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	// 去除越级目录
	segs := strings.Split(p, "/")
	for i, st := range segs {
		if st == ".." || st == "" {
			segs[i] = "_"
		}
	}
	p = strings.Join(segs, "/")
	return fmt.Sprintf("versions/%s/%s/%s", versionID, utils.NewID("f"), p)
}
