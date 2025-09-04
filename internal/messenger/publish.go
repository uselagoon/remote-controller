package messenger

import (
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
)

// Publish publishes a message to a given queue
func (m *Messenger) Publish(queue string, message []byte) error {
	opLog := ctrl.Log.WithName("handlers").WithName("LagoonTasks")

	// reuse the existing connection from the messenger when publishing to prevent needing to establish a new connection every time
	producer, err := m.MQ.AsyncProducer(queue)
	if err != nil {
		opLog.Info(fmt.Sprintf("Failed to get async producer: %v", err))
		return err
	}
	producer.Produce(message)
	return nil
}
