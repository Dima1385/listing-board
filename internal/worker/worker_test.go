package worker

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"listingboard/internal/board"
)

func TestProcessWalksEveryStepToSuccess(t *testing.T) {
	store, dataDir := newFixture(t)
	job := store.add(t, dataDir, true)

	if err := New(store, dataDir, time.Millisecond).Process(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	got := store.job(job.ID)
	if got.Status != board.StatusSuccess || got.RunID != job.RunID || got.Error != "" {
		t.Fatalf("sheet is %s with run %q and error %q", got.Status, got.RunID, got.Error)
	}
	want := []string{"done pending pending", "done done pending", "done done done", "done done done"}
	if len(store.saves) != len(want) {
		t.Fatalf("saves %v, want %v", store.saves, want)
	}
	for i := range want {
		if store.saves[i] != want[i] {
			t.Fatalf("save %d is %q, want %q", i, store.saves[i], want[i])
		}
	}
}

func TestProcessResumesFromTheFirstPendingStep(t *testing.T) {
	store, dataDir := newFixture(t)
	job := store.add(t, dataDir, true)
	job.Checklist[0].State = board.StepDone
	store.put(job)

	if err := New(store, dataDir, time.Millisecond).Process(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if got := store.job(job.ID); got.Status != board.StatusSuccess || got.RunID != job.RunID {
		t.Fatalf("sheet is %s with run %q, want success with run %q", got.Status, got.RunID, job.RunID)
	}
	if store.saves[0] != "done done pending" {
		t.Fatalf("first save %q: a done step was done again", store.saves[0])
	}
}

func TestResumeKeepsThePauseAfterTheLastSave(t *testing.T) {
	store, dataDir := newFixture(t)
	job := store.add(t, dataDir, true)
	job.Checklist[0].State = board.StepDone
	job.UpdatedAt = time.Now().UTC()
	store.put(job)
	const pause = 200 * time.Millisecond
	var firstSave time.Time
	store.afterSave = func() {
		if firstSave.IsZero() {
			firstSave = time.Now()
		}
	}

	if err := New(store, dataDir, pause).Process(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if waited := firstSave.Sub(job.UpdatedAt); waited < pause {
		t.Fatalf("next step came %s after the last save, want at least %s", waited, pause)
	}
}

func TestProcessFailsWhenTheBrochureIsMissing(t *testing.T) {
	store, dataDir := newFixture(t)
	job := store.add(t, dataDir, false)

	if err := New(store, dataDir, time.Millisecond).Process(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	got := store.job(job.ID)
	if got.Status != board.StatusFailed || got.Error != "Brochure is missing" {
		t.Fatalf("sheet is %s with error %q", got.Status, got.Error)
	}
	for _, step := range got.Checklist {
		if step.State != board.StepPending {
			t.Fatalf("step %q is %s after a failure", step.Label, step.State)
		}
	}
}

func TestStoppingMidRunLeavesTheSheetRunning(t *testing.T) {
	store, dataDir := newFixture(t)
	job := store.add(t, dataDir, true)
	ctx, cancel := context.WithCancel(context.Background())
	store.afterSave = cancel

	if err := New(store, dataDir, time.Hour).Process(ctx, job.ID); err == nil {
		t.Fatal("expected the cancellation to stop the run")
	}
	got := store.job(job.ID)
	if got.Status != board.StatusRunning || got.RunID != job.RunID {
		t.Fatalf("sheet is %s with run %q, want running with run %q", got.Status, got.RunID, job.RunID)
	}
	if got.Checklist[0].State != board.StepDone || got.Checklist[1].State != board.StepPending {
		t.Fatalf("checklist %v, want only the first step done", got.Checklist)
	}
}

func TestRunPicksUpRunningSheets(t *testing.T) {
	store, dataDir := newFixture(t)
	job := store.add(t, dataDir, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		New(store, dataDir, time.Millisecond).Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for store.job(job.ID).Status != board.StatusSuccess {
		if time.Now().After(deadline) {
			t.Fatalf("sheet is still %s", store.job(job.ID).Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

type fakeStore struct {
	mu        sync.Mutex
	jobs      map[string]board.Job
	saves     []string
	afterSave func()
}

func newFixture(t *testing.T) (*fakeStore, string) {
	return &fakeStore{jobs: map[string]board.Job{}}, t.TempDir()
}

func (f *fakeStore) add(t *testing.T, dataDir string, withBrochure bool) board.Job {
	t.Helper()
	job := board.Job{
		ID:        board.NewID(),
		UserID:    board.NewID(),
		Status:    board.StatusRunning,
		RunID:     board.NewID(),
		Filename:  "brochure.pdf",
		Checklist: board.NewChecklist(),
	}
	if withBrochure {
		path := board.BrochurePath(dataDir, job.UserID, job.ID)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("%PDF-1.4"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.put(job)
	return job
}

func (f *fakeStore) put(job board.Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[job.ID] = clone(job)
}

func (f *fakeStore) job(id string) board.Job {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.jobs[id])
}

func (f *fakeStore) RunningJobIDs(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for id, job := range f.jobs {
		if job.Status == board.StatusRunning {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (f *fakeStore) Job(_ context.Context, id string) (board.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	job, ok := f.jobs[id]
	if !ok {
		return board.Job{}, board.ErrNotFound
	}
	return clone(job), nil
}

func (f *fakeStore) UpdateRun(_ context.Context, job board.Job) error {
	f.mu.Lock()
	current, ok := f.jobs[job.ID]
	if !ok || current.Status != board.StatusRunning || current.RunID != job.RunID {
		f.mu.Unlock()
		return board.ErrConflict
	}
	f.jobs[job.ID] = clone(job)
	states := ""
	for i, step := range job.Checklist {
		if i > 0 {
			states += " "
		}
		states += step.State
	}
	f.saves = append(f.saves, states)
	afterSave := f.afterSave
	f.mu.Unlock()
	if afterSave != nil {
		afterSave()
	}
	return nil
}

func clone(job board.Job) board.Job {
	job.Checklist = append([]board.Step(nil), job.Checklist...)
	return job
}
