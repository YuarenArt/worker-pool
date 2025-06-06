package workerpool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Task определяет интерфейс для задач, обрабатываемых воркерами.
type Task interface {
	Process(workerID int)
}

// StringTask — простая реализация задачи для демонстрации.
type StringTask string

// Process выполняет задачу с указанным ID воркера.
func (s StringTask) Process(workerID int) {
	fmt.Printf("Worker %d processing task: %s\n", workerID, s)
}

// WorkerPool управляет пулом воркеров для обработки задач.
type WorkerPool struct {
	tasks         chan Task          // Канал для отправки задач
	addWorker     chan struct{}      // Канал для сигнала добавления воркера
	rmWorker      chan struct{}      // Канал для сигнала удаления воркера
	wg            sync.WaitGroup     // WaitGroup для отслеживания завершения воркеров
	workerID      atomic.Int32       // Атомарный счетчик для уникальных ID воркеров
	workerCount   atomic.Int32       // Атомарный счетчик активных воркеров
	ctx           context.Context    // Контекст для graceful shutdown
	cancel        context.CancelFunc // Функция отмены для остановки пула
	maxWorkers    int32              // Максимальное количество воркеров
	closed        atomic.Bool        // Флаг, указывающий, закрыт ли пул
	workerCancels sync.Map           // Хранилище функций отмены для каждого воркера
}

// Option определяет функцию для настройки параметров пула воркеров.
type Option func(*WorkerPool) error

// WithMaxWorkers задает максимальное количество воркеров.
func WithMaxWorkers(maxWorkers int) Option {
	return func(wp *WorkerPool) error {
		if maxWorkers < 0 {
			return fmt.Errorf("maxWorkers cannot be negative: %d", maxWorkers)
		}
		wp.maxWorkers = int32(maxWorkers)
		return nil
	}
}

// WithTaskBufferSize задает размер буфера для канала задач.
func WithTaskBufferSize(bufferSize int) Option {
	return func(wp *WorkerPool) error {
		if bufferSize < 0 {
			return fmt.Errorf("bufferSize cannot be negative: %d", bufferSize)
		}
		wp.tasks = make(chan Task, bufferSize)
		return nil
	}
}

// WithControlBufferSize задает размер буфера для каналов управления воркерами.
func WithControlBufferSize(controlBufferSize int) Option {
	return func(wp *WorkerPool) error {
		if controlBufferSize < 0 {
			return fmt.Errorf("controlBufferSize cannot be negative: %d", controlBufferSize)
		}
		wp.addWorker = make(chan struct{}, controlBufferSize)
		wp.rmWorker = make(chan struct{}, controlBufferSize)
		return nil
	}
}

// NewWorkerPool создает новый пул воркеров с использованием паттерна опций.
func NewWorkerPool(ctx context.Context, opts ...Option) (*WorkerPool, error) {
	// Создание контекста с функцией отмены
	ctx, cancel := context.WithCancel(ctx)

	// Инициализация пула с значениями по умолчанию
	wp := &WorkerPool{
		tasks:      make(chan Task),        // Канал задач по умолчанию без буфера
		addWorker:  make(chan struct{}, 1), // Буфер по умолчанию для избежания блокировки
		rmWorker:   make(chan struct{}, 1), // Буфер по умолчанию для избежания блокировки
		ctx:        ctx,
		cancel:     cancel,
		maxWorkers: 10, // Максимальное количество воркеров по умолчанию
	}

	for _, opt := range opts {
		if err := opt(wp); err != nil {
			cancel()
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	if wp.tasks == nil {
		wp.tasks = make(chan Task)
	}

	// Запуск диспетчера для управления воркерами
	go wp.dispatch()

	return wp, nil
}

// startWorker запускает нового воркера с уникальным ID.
func (wp *WorkerPool) startWorker() {
	workerID := int(wp.workerID.Add(1))
	wp.workerCount.Add(1)
	wp.wg.Add(1)

	// Создание индивидуального контекста для воркера
	workerCtx, workerCancel := context.WithCancel(wp.ctx)
	wp.workerCancels.Store(workerID, workerCancel)

	go func() {
		defer func() {
			wp.workerCount.Add(-1)
			wp.workerCancels.Delete(workerID)
			workerCancel() // Явно вызываем cancel для очистки
			wp.wg.Done()
			fmt.Printf("Worker %d stopped\n", workerID)
		}()
		for {
			select {
			case <-workerCtx.Done():
				return
			case task := <-wp.tasks:
				if task != nil { // Проверка на nil для безопасности
					task.Process(workerID)
				}
			}
		}
	}()
}

// dispatch управляет добавлением и удалением воркеров.
func (wp *WorkerPool) dispatch() {
	for {
		select {
		case <-wp.ctx.Done():
			// Остановка всех воркеров перед выходом
			wp.workerCancels.Range(func(key, value interface{}) bool {
				if cf, ok := value.(context.CancelFunc); ok {
					cf()
				}
				return true
			})
			return
		case <-wp.addWorker:
			if wp.workerCount.Load() < wp.maxWorkers {
				wp.startWorker()
				fmt.Printf("Added worker, total: %d\n", wp.workerCount.Load())
			} else {
				fmt.Println("Cannot add worker: maximum limit reached")
			}
		case <-wp.rmWorker:
			if wp.workerCount.Load() > 0 {
				var cancelFunc context.CancelFunc
				wp.workerCancels.Range(func(key, value interface{}) bool {
					if cf, ok := value.(context.CancelFunc); ok {
						cancelFunc = cf
						wp.workerCancels.Delete(key)
						return false // Прерываем итерацию
					}
					return true
				})
				if cancelFunc != nil {
					cancelFunc()
					fmt.Printf("Requested worker removal, total: %d\n", wp.workerCount.Load()-1)
				} else {
					fmt.Println("No worker available to stop")
				}
			} else {
				fmt.Println("Cannot remove worker: no workers available")
			}
		}
	}
}

// AddWorker запрашивает добавление нового воркера в пул.
func (wp *WorkerPool) AddWorker() error {
	if wp.closed.Load() {
		return fmt.Errorf("cannot add worker: pool is shut down")
	}
	select {
	case wp.addWorker <- struct{}{}:
		return nil
	case <-wp.ctx.Done():
		return fmt.Errorf("cannot add worker: pool is shutting down")
	}
}

// RemoveWorker запрашивает удаление воркера из пула.
func (wp *WorkerPool) RemoveWorker() error {
	if wp.closed.Load() {
		return fmt.Errorf("cannot remove worker: pool is shut down")
	}
	select {
	case wp.rmWorker <- struct{}{}:
		return nil
	case <-wp.ctx.Done():
		return fmt.Errorf("cannot remove worker: pool is shutting down")
	}
}

// Submit отправляет задачу в пул воркеров для обработки.
func (wp *WorkerPool) Submit(task Task) error {
	if wp.closed.Load() {
		return fmt.Errorf("cannot submit task: pool is shut down")
	}
	if task == nil {
		return fmt.Errorf("cannot submit nil task")
	}
	select {
	case wp.tasks <- task:
		return nil
	case <-wp.ctx.Done():
		return fmt.Errorf("cannot submit task: pool is shutting down")
	default:
		return fmt.Errorf("cannot submit task: task queue is full")
	}
}

// SubmitBlocking отправляет задачу в пул воркеров для обработки, блокируя, если очередь заполнена.
func (wp *WorkerPool) SubmitBlocking(task Task) error {
	if wp.closed.Load() {
		return fmt.Errorf("cannot submit task: pool is shut down")
	}
	select {
	case wp.tasks <- task:
		return nil
	case <-wp.ctx.Done():
		return fmt.Errorf("cannot submit task: pool is shutting down")
	}
}

// Shutdown останавливает пул и ожидает завершения всех воркеров.
func (wp *WorkerPool) Shutdown() {
	if wp.closed.Swap(true) {
		return
	}
	wp.cancel()
	// Ожидание завершения всех воркеров
	wp.wg.Wait()
	// Закрытие каналов
	close(wp.tasks)
	close(wp.addWorker)
	close(wp.rmWorker)
	// Очистка всех функций cancel
	wp.workerCancels.Range(func(key, value interface{}) bool {
		if cf, ok := value.(context.CancelFunc); ok {
			cf()
		}
		wp.workerCancels.Delete(key)
		return true
	})
}

// WorkerCount возвращает текущее количество активных воркеров.
func (wp *WorkerPool) WorkerCount() int {
	return int(wp.workerCount.Load())
}
