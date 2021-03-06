package queue

import (
	"context"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/vmihailenco/taskq/v3"
	"github.com/vmihailenco/taskq/v3/redisq"
)

var log = logrus.WithField("package", "pkg/queue")

type Queue struct {
	redis        redis.UniversalClient
	main         taskq.Queue
	ennoblements taskq.Queue
	factory      taskq.Factory
}

func New(cfg *Config) (*Queue, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	q := &Queue{
		redis: cfg.Redis,
	}

	if err := q.init(cfg); err != nil {
		return nil, err
	}

	return q, nil
}

func (q *Queue) init(cfg *Config) error {
	q.factory = redisq.NewFactory()
	q.main = q.registerQueue("main", cfg.WorkerLimit)
	q.ennoblements = q.registerQueue("ennoblements", cfg.WorkerLimit)

	if err := registerTasks(&registerTasksConfig{
		DB:    cfg.DB,
		Queue: q,
	}); err != nil {
		return errors.Wrapf(err, "couldn't register tasks")
	}

	return nil
}

func (q *Queue) registerQueue(name string, limit int) taskq.Queue {
	return q.factory.RegisterQueue(&taskq.QueueOptions{
		Name:               name,
		ReservationTimeout: time.Minute * 2,
		Redis:              q.redis,
		MinNumWorker:       int32(limit),
		MaxNumWorker:       int32(limit),
	})
}

func (q *Queue) getQueueByTaskName(name string) taskq.Queue {
	switch name {
	case LoadVersionsAndUpdateServerData,
		LoadServersAndUpdateData,
		UpdateServerData,
		Vacuum,
		VacuumServerData,
		UpdateHistory,
		UpdateServerHistory,
		UpdateStats,
		UpdateServerStats,
		DeleteNonExistentVillages,
		ServerDeleteNonExistentVillages:
		return q.main
	case UpdateEnnoblements,
		UpdateServerEnnoblements:
		return q.ennoblements
	}
	return nil
}

func (q *Queue) Start(ctx context.Context) error {
	if err := q.factory.StartConsumers(ctx); err != nil {
		return errors.Wrap(err, "couldn't start the queue")
	}
	return nil
}

func (q *Queue) Close() error {
	if err := q.factory.Close(); err != nil {
		return errors.Wrap(err, "couldn't close the queue")
	}
	return nil
}

func (q *Queue) Add(msg *taskq.Message) error {
	queue := q.getQueueByTaskName(msg.TaskName)
	if queue == nil {
		return errors.Errorf("couldn't add the message to the queue: unknown task name '%s'", msg.TaskName)
	}
	if err := queue.Add(msg); err != nil {
		return errors.Wrap(err, "couldn't add the message to the queue")
	}
	return nil
}
