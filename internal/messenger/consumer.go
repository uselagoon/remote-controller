package messenger

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cheshir/go-mq"
	"gopkg.in/matryer/try.v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Consumer handles consuming messages sent to the queue that these controllers are connected to and processes them accordingly
func (m *Messenger) Consumer(targetName string) { //error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	ctx := context.Background()
	var messageQueue mq.MQ
	// if no mq is found when the goroutine starts, retry a few times before exiting
	// default is 10 retry with 30 second delay = 5 minutes
	err := try.Do(func(attempt int) (bool, error) {
		var err error
		messageQueue, err = mq.New(m.Config)
		if err != nil {
			opLog.Error(err,
				fmt.Sprintf(
					"Failed to initialize message queue manager, retrying in %d seconds, attempt %d/%d",
					m.ConnectionRetryInterval,
					attempt,
					m.ConnectionAttempts,
				),
			)
			time.Sleep(time.Duration(m.ConnectionRetryInterval) * time.Second)
		}
		return attempt < m.ConnectionAttempts, err
	})
	if err != nil {
		log.Fatalf("Finally failed to initialize message queue manager: %v", err)
	}
	defer messageQueue.Close()

	go func() {
		count := 0
		for err := range messageQueue.Error() {
			opLog.Info(fmt.Sprintf("Caught error from message queue: %v", err))
			// if there are 5 errors (usually after about 60-120 seconds)
			// fatalf and restart the controller
			if count == 4 {
				log.Fatalf("Terminating controller due to error with message queue: %v", err)
			}
			count++
		}
		count = 0
	}()

	forever := make(chan bool)

	// if this controller is set up for single queue only, then only start the single queue listener
	if m.EnableSingleQueue {
		// Handle any tasks that go to the `lagoon-controller` queue
		opLog.Info(fmt.Sprintf("Listening for lagoon-controller:%s", targetName))
		err = messageQueue.SetConsumerHandler("controller-queue", func(message mq.Message) {
			if err == nil {
				if err := m.handleLagoonEvent(ctx, opLog, message.Body()); err != nil {
					//@TODO: send msg back to lagoon and update task to failed?
					message.Ack(false) // ack to remove from queue
					return
				}
			}
			message.Ack(false) // ack to remove from queue
		})
		if err != nil {
			log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "builddeploy-queue", err))
		}
	}
	// Handle any tasks that go to the `builddeploy` queue
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:builddeploy", targetName))
	err = messageQueue.SetConsumerHandler("builddeploy-queue", func(message mq.Message) {
		if err == nil {
			if err := m.handleBuildEvent(ctx, opLog, message.Body()); err != nil {
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "builddeploy-queue", err))
	}

	// Handle any tasks that go to the `remove` queue
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:remove", targetName))
	err = messageQueue.SetConsumerHandler("remove-queue", func(message mq.Message) {
		if err == nil {
			if err := m.handleRemovalEvent(ctx, opLog, message.Body()); err != nil {
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "remove-queue", err))
	}

	// Handle any tasks that go to the `jobs` queue
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:jobs", targetName))
	err = messageQueue.SetConsumerHandler("jobs-queue", func(message mq.Message) {
		if err == nil {
			if err := m.handleTaskEvent(ctx, opLog, message.Body()); err != nil {
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "jobs-queue", err))
	}

	// Handle any tasks that go to the `misc` queue
	opLog.Info(fmt.Sprintf("Listening for lagoon-tasks:%s:misc", targetName))
	err = messageQueue.SetConsumerHandler("misc-queue", func(message mq.Message) {
		if err == nil {
			if err := m.handleMiscEvent(ctx, opLog, message.Body()); err != nil {
				//@TODO: send msg back to lagoon and update task to failed?
				message.Ack(false) // ack to remove from queue
				return
			}
		}
		message.Ack(false) // ack to remove from queue
	})
	if err != nil {
		log.Fatalf(fmt.Sprintf("Failed to set handler to consumer `%s`: %v", "misc-queue", err))
	}
	<-forever
}
