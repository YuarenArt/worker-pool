# Worker Pool (Go)

Примитивный worker-pool на Go с возможностью динамически добавлять и удалять воркеры.

## Возможности
- Динамическое добавление и удаление воркеров
- Обработка задач (строк) через канал
- Безопасная работа с горутинами и каналами
- Грейсфул-шатдаун пула

## Пример использования
```go
package main

import (
    "context"
    "time"
    "workerpool/pkg/worker_pool"
)

func main() {
    ctx := context.Background()
    pool, _ := workerpool.NewWorkerPool(ctx, workerpool.WithMaxWorkers(5))
    defer pool.Shutdown()

    // Добавляем воркеров
    pool.AddWorker()
    pool.AddWorker()

    // Отправляем задачи
    pool.Submit(workerpool.StringTask("task 1"))
    pool.Submit(workerpool.StringTask("task 2"))

    time.Sleep(time.Second) // Ждём обработки
}
```

## Запуск тестов
```sh
go test ./pkg/worker_pool/...
```

---

**Задание:** Реализовать worker-pool с возможностью динамически добавлять и удалять воркеры. Входные данные поступают в канал, воркеры их обрабатывают (например, выводят на экран номер воркера и сами данные). 