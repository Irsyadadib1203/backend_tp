package worker

import (
	"context"
	"log"
	"sync"
	"time"
)

// Task is a function to be executed by the worker pool
type Task func()

// WorkerPool manages a bounded set of worker goroutines and a task queue
type WorkerPool struct {
	maxWorkers   int
	taskQueue    chan Task
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	shutdownOnce sync.Once
}

// GlobalPool is the app-wide worker pool for async transaction fulfillment and webhook delivery
var GlobalPool = NewWorkerPool(30, 1000)

// NewWorkerPool creates and starts a bounded worker pool
func NewWorkerPool(maxWorkers, queueSize int) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &WorkerPool{
		maxWorkers: maxWorkers,
		taskQueue:  make(chan Task, queueSize),
		ctx:        ctx,
		cancel:     cancel,
	}

	p.start()
	return p
}

func (p *WorkerPool) start() {
	for i := 0; i < p.maxWorkers; i++ {
		p.wg.Add(1)
		go func(workerID int) {
			defer p.wg.Done()
			for {
				select {
				case <-p.ctx.Done():
					// Drain remaining tasks before exiting
					for {
						select {
						case task, ok := <-p.taskQueue:
							if !ok {
								return
							}
							p.safeExecute(task)
						default:
							return
						}
					}
				case task, ok := <-p.taskQueue:
					if !ok {
						return
					}
					p.safeExecute(task)
				}
			}
		}(i + 1)
	}
}

func (p *WorkerPool) safeExecute(task Task) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WorkerPool] Recovered from panic in worker task: %v", r)
		}
	}()
	task()
}

// Submit sends a task to the queue. Returns true if accepted, false if queue is full.
func (p *WorkerPool) Submit(task Task) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.taskQueue <- task:
		return true
	default:
		// Queue full: execute in a fallback goroutine to avoid dropping transaction, but log warning
		log.Printf("[WorkerPool] Task queue full (size: %d). Spawning fallback worker.", cap(p.taskQueue))
		go p.safeExecute(task)
		return true
	}
}

// Stop gracefully shuts down the worker pool and waits for queued tasks to finish with timeout
func (p *WorkerPool) Stop(timeout time.Duration) {
	p.shutdownOnce.Do(func() {
		p.cancel()
		close(p.taskQueue)

		c := make(chan struct{})
		go func() {
			defer close(c)
			p.wg.Wait()
		}()

		select {
		case <-c:
			log.Println("[WorkerPool] All workers stopped cleanly.")
		case <-time.After(timeout):
			log.Printf("[WorkerPool] Timed out waiting for workers after %v", timeout)
		}
	})
}
