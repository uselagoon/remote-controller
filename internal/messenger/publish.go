package messenger

import (
	"fmt"

	"github.com/cheshir/go-mq"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Publish publishes a message to a given queue
func (m *Messenger) Publish(queue string, message []byte) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")
	// no need to re-try here, this is on a cron schedule and the error is returned, cron will try again whenever it is set to run next
	messageQueue, err := mq.New(m.Config)
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to initialize message queue manager: %v", err))
		return err
	}
	defer messageQueue.Close()

	producer, err := messageQueue.AsyncProducer(queue)
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to get async producer: %v", err))
		return err
	}
	producer.Produce([]byte(fmt.Sprintf("%s", message)))
	return nil
}
