package workerpool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// mockTask реализует интерфейс Task для тестирования.
type mockTask struct {
	id        int
	processed atomic.Bool
	workerID  int
}

// Process имитирует обработку задачи и сохраняет ID воркера.
func (m *mockTask) Process(workerID int) {
	m.workerID = workerID
	m.processed.Store(true)
}

// WorkerPoolTest определяет набор тестов для worker pool.
type WorkerPoolTest struct {
	suite.Suite
	ctx context.Context
}

func (s *WorkerPoolTest) SetupTest() {
	s.ctx = context.Background()
}

func (s *WorkerPoolTest) TestValidCreation() {
	wp, err := NewWorkerPool(s.ctx,
		WithMaxWorkers(5),
		WithTaskBufferSize(10),
		WithControlBufferSize(2),
	)
	s.Require().NoError(err, "Expected successful pool creation")
	defer wp.Shutdown()

	// Add initial workers explicitly
	s.NoError(wp.AddWorker())
	s.NoError(wp.AddWorker())
	time.Sleep(50 * time.Millisecond)
	s.Equal(2, wp.WorkerCount(), "Expected 2 active workers")
}

func (s *WorkerPoolTest) TestInvalidCreation() {
	_, err := NewWorkerPool(s.ctx, WithTaskBufferSize(-1))
	s.Error(err, "Expected error for negative bufferSize")
	s.Contains(err.Error(), "bufferSize cannot be negative")
}

func (s *WorkerPoolTest) TestAddRemoveWorkers() {
	wp, err := NewWorkerPool(s.ctx, WithMaxWorkers(3))
	s.Require().NoError(err)
	defer wp.Shutdown()

	// Add first worker
	s.NoError(wp.AddWorker())
	time.Sleep(50 * time.Millisecond)
	s.Equal(1, wp.WorkerCount(), "Expected 1 worker after first addition")

	s.NoError(wp.AddWorker())
	time.Sleep(50 * time.Millisecond)
	s.Equal(2, wp.WorkerCount(), "Expected 2 workers after second addition")

	s.NoError(wp.RemoveWorker())
	time.Sleep(50 * time.Millisecond)
	s.Equal(1, wp.WorkerCount(), "Expected 1 worker after removal")

	s.NoError(wp.RemoveWorker())
	time.Sleep(50 * time.Millisecond)
	s.Equal(0, wp.WorkerCount(), "Expected 0 workers after second removal")
}

func (s *WorkerPoolTest) TestExceedingMaxWorkers() {
	wp, err := NewWorkerPool(s.ctx, WithMaxWorkers(1))
	s.Require().NoError(err)
	defer wp.Shutdown()

	s.NoError(wp.AddWorker())
	time.Sleep(50 * time.Millisecond)
	s.Equal(1, wp.WorkerCount(), "Worker count should not exceed maximum")

	s.NoError(wp.AddWorker())
	time.Sleep(50 * time.Millisecond)
	s.Equal(1, wp.WorkerCount(), "Worker count should not exceed maximum")
}

func (s *WorkerPoolTest) TestTaskSubmissionAndProcessing() {
	wp, err := NewWorkerPool(s.ctx, WithTaskBufferSize(2))
	s.Require().NoError(err)
	defer wp.Shutdown()

	// Add workers explicitly
	s.NoError(wp.AddWorker())
	s.NoError(wp.AddWorker())

	tasks := []*mockTask{{id: 1}, {id: 2}}
	for _, task := range tasks {
		s.NoError(wp.Submit(task))
	}
	time.Sleep(50 * time.Millisecond)
	for _, task := range tasks {
		s.True(task.processed.Load(), "Task %d should be processed", task.id)
		s.Greater(task.workerID, 0, "Worker ID for task %d should be positive", task.id)
	}
}

func (s *WorkerPoolTest) TestTaskBufferOverflow() {
	wp, err := NewWorkerPool(s.ctx, WithTaskBufferSize(1))
	s.Require().NoError(err)
	defer wp.Shutdown()

	s.NoError(wp.Submit(&mockTask{id: 1}))
	err = wp.Submit(&mockTask{id: 2})
	s.Error(err, "Expected error on buffer overflow")
	s.Contains(err.Error(), "task queue is full")
}

func (s *WorkerPoolTest) TestPostShutdownOperations() {
	wp, err := NewWorkerPool(s.ctx)
	s.Require().NoError(err)
	wp.Shutdown()
	time.Sleep(50 * time.Millisecond)

	s.Error(wp.AddWorker())
	s.Contains(wp.AddWorker().Error(), "pool is shut down")

	s.Error(wp.RemoveWorker())
	s.Contains(wp.RemoveWorker().Error(), "pool is shut down")

	s.Error(wp.Submit(&mockTask{id: 1}))
	s.Contains(wp.Submit(&mockTask{id: 1}).Error(), "pool is shut down")
}

func (s *WorkerPoolTest) TestConcurrentAddRemoveWorkers() {
	wp, err := NewWorkerPool(s.ctx, WithMaxWorkers(5))
	s.Require().NoError(err)
	defer wp.Shutdown()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = wp.AddWorker()
		}()
		go func() {
			defer wg.Done()
			_ = wp.RemoveWorker()
		}()
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	count := wp.WorkerCount()
	s.GreaterOrEqual(count, 0)
	s.LessOrEqual(count, 5)
}

func (s *WorkerPoolTest) TestConcurrentTaskSubmission() {
	wp, err := NewWorkerPool(s.ctx, WithTaskBufferSize(10))
	s.Require().NoError(err)
	defer wp.Shutdown()

	// Add workers explicitly
	s.NoError(wp.AddWorker())
	s.NoError(wp.AddWorker())

	tasks := make([]*mockTask, 10)
	for i := 0; i < 10; i++ {
		tasks[i] = &mockTask{id: i + 1}
	}

	var wg sync.WaitGroup
	for _, task := range tasks {
		wg.Add(1)
		go func(task *mockTask) {
			defer wg.Done()
			s.NoError(wp.Submit(task))
		}(task)
	}
	wg.Wait()
	time.Sleep(50 * time.Millisecond)
	for _, task := range tasks {
		s.True(task.processed.Load(), "Task %d should be processed", task.id)
	}
}

func (s *WorkerPoolTest) TestShutdownWithActiveTasks() {
	wp, err := NewWorkerPool(s.ctx, WithTaskBufferSize(2))
	s.Require().NoError(err)

	// Add worker explicitly
	s.NoError(wp.AddWorker())

	s.NoError(wp.Submit(&mockTask{id: 1}))
	wp.Shutdown()
	time.Sleep(50 * time.Millisecond)
	s.Equal(0, wp.WorkerCount(), "Expected all workers to shut down")
}

func (s *WorkerPoolTest) TestSubmitBlocksWhenQueueFull() {
	wp, err := NewWorkerPool(s.ctx, WithTaskBufferSize(1))
	s.Require().NoError(err)
	defer wp.Shutdown()

	task1 := &mockTask{id: 1}
	task2 := &mockTask{id: 2}

	err = wp.Submit(task1)
	s.NoError(err)

	err = wp.Submit(task2)
	s.Error(err)
	s.Contains(err.Error(), "task queue is full")
}

func (s *WorkerPoolTest) TestMultipleShutdowns() {
	wp, err := NewWorkerPool(s.ctx)
	s.Require().NoError(err)
	wp.Shutdown()
	s.NotPanics(func() { wp.Shutdown() })
}

func (s *WorkerPoolTest) TestWithControlBufferSizeNegative() {
	_, err := NewWorkerPool(s.ctx, WithControlBufferSize(-1))
	s.Error(err)
	s.Contains(err.Error(), "controlBufferSize cannot be negative")
}

func (s *WorkerPoolTest) TestSubmitNilTask() {
	wp, err := NewWorkerPool(s.ctx)
	s.Require().NoError(err)
	defer wp.Shutdown()
	s.Error(wp.Submit(nil))
}

func (s *WorkerPoolTest) TestSubmitBlockingSuccess() {
	wp, err := NewWorkerPool(s.ctx, WithTaskBufferSize(1))
	s.Require().NoError(err)
	defer wp.Shutdown()

	// Add worker explicitly
	s.NoError(wp.AddWorker())

	task := &mockTask{id: 1}
	err = wp.SubmitBlocking(task)
	s.NoError(err, "SubmitBlocking should succeed when buffer is available")
}

func (s *WorkerPoolTest) TestSubmitBlocking_ContextDone() {
	ctx, cancel := context.WithCancel(context.Background())
	wp, err := NewWorkerPool(ctx, WithTaskBufferSize(0))
	s.Require().NoError(err)
	defer wp.Shutdown()

	// Add worker explicitly
	s.NoError(wp.AddWorker())

	// Заполняем очередь, чтобы блокировать отправку
	task1 := &mockTask{id: 3}
	_ = wp.SubmitBlocking(task1)

	// Завершаем контекст, чтобы сработал <-wp.ctx.Done()
	cancel()

	task2 := &mockTask{id: 4}
	err = wp.SubmitBlocking(task2)
	s.Error(err)
	s.Contains(err.Error(), "pool is shutting down")
}

func TestWorkerPoolSuite(t *testing.T) {
	suite.Run(t, new(WorkerPoolTest))
}
