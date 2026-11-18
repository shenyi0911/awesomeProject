package implement

import (
	"reflect"
	"testing"

	"awesomeProject/internal/domain/do/entity"
)

func TestReplaceFileNoDuplicate(t *testing.T) {
	files := []entity.AlgorithmFile{{Name: "b.txt", Path: "k/b"}}
	out, superseded := replaceFile(files, entity.AlgorithmFile{Name: "a.txt", Path: "k/a"})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Name != "b.txt" || out[1].Name != "a.txt" {
		t.Fatalf("unexpected order: %+v", out)
	}
	if len(superseded) != 0 {
		t.Fatalf("superseded = %v, want empty", superseded)
	}
}

func TestReplaceFileReplacesSameName(t *testing.T) {
	files := []entity.AlgorithmFile{
		{Name: "a.txt", Path: "k/a-old"},
		{Name: "b.txt", Path: "k/b"},
	}
	out, superseded := replaceFile(files, entity.AlgorithmFile{Name: "a.txt", Path: "k/a-new"})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Name != "b.txt" || out[1].Path != "k/a-new" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if !reflect.DeepEqual(superseded, []string{"k/a-old"}) {
		t.Fatalf("superseded = %v, want [k/a-old]", superseded)
	}
}

func TestReplaceFileRemovesAllPrecedingDuplicates(t *testing.T) {
	files := []entity.AlgorithmFile{
		{Name: "a.txt", Path: "k/a-1"},
		{Name: "a.txt", Path: "k/a-2"},
		{Name: "b.txt", Path: "k/b"},
	}
	out, superseded := replaceFile(files, entity.AlgorithmFile{Name: "a.txt", Path: "k/a-3"})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Name != "b.txt" || out[1].Path != "k/a-3" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if !reflect.DeepEqual(superseded, []string{"k/a-1", "k/a-2"}) {
		t.Fatalf("superseded = %v, want [k/a-1 k/a-2]", superseded)
	}
}

func TestReplaceFileOnEmptyList(t *testing.T) {
	out, superseded := replaceFile(nil, entity.AlgorithmFile{Name: "a.txt", Path: "k/a"})
	if len(out) != 1 || out[0].Path != "k/a" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if superseded != nil {
		t.Fatalf("superseded = %v, want nil", superseded)
	}
}
