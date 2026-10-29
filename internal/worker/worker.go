package worker

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"listingboard/internal/board"
)

const (
	pollInterval    = time.Second
	brochureMissing = "Brochure is missing"
)

type Store interface {
	RunningJobIDs(ctx context.Context) ([]string, error)
	Job(ctx context.Context, id string) (board.Job, error)
	UpdateRun(ctx context.Context, job board.Job) error
}

type Worker struct {
	store   Store
	dataDir string
	pause   time.Duration

	mu     sync.Mutex
	active map[string]struct{}
}

func New(store Store, dataDir string, pause time.Duration) *Worker {
	return &Worker{store: store, dataDir: dataDir, pause: pause, active: map[string]struct{}{}}
}

func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		w.dispatch(ctx, &wg)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) dispatch(ctx context.Context, wg *sync.WaitGroup) {
	ids, err := w.store.RunningJobIDs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("list running sheets: %v", err)
		}
		return
	}
	for _, id := range ids {
		if !w.claim(id) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer w.release(id)
			if err := w.Process(ctx, id); err != nil && ctx.Err() == nil {
				log.Printf("sheet %s: %v", id, err)
			}
		}()
	}
}

func (w *Worker) Process(ctx context.Context, id string) error {
	job, err := w.store.Job(ctx, id)
	if err != nil {
		return err
	}
	for job.Status == board.StatusRunning {
		next := firstPending(job.Checklist)
		if next > 0 {
			if err := sleep(ctx, time.Until(job.UpdatedAt.Add(w.pause))); err != nil {
				return err
			}
		}
		switch {
		case next < 0:
			job.Status = board.StatusSuccess
		case !w.brochureReadable(job):
			job.Status = board.StatusFailed
			job.Error = brochureMissing
		default:
			job.Checklist[next].State = board.StepDone
		}
		job.UpdatedAt = time.Now().UTC()
		if err := w.store.UpdateRun(ctx, job); errors.Is(err, board.ErrConflict) {
			return nil
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) brochureReadable(job board.Job) bool {
	_, err := os.ReadFile(board.BrochurePath(w.dataDir, job.UserID, job.ID))
	return err == nil
}

func (w *Worker) claim(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, busy := w.active[id]; busy {
		return false
	}
	w.active[id] = struct{}{}
	return true
}

func (w *Worker) release(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.active, id)
}

func firstPending(steps []board.Step) int {
	for i, step := range steps {
		if step.State != board.StepDone {
			return i
		}
	}
	return -1
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
