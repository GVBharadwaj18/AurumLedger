package rabbitmq

import (
	"context"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	EventsExchange = "aurumledger.events"
	DLXExchange    = "aurumledger.dlx"
	TransferQueue  = "aurumledger.wallet_transfers"
	DLQQueue       = "aurumledger.wallet_transfers.dlq"
	TransferRoutingKey = "transfer.completed"
	DLQRoutingKey      = "transfer.dlq"
)

type RabbitMQ struct {
	conn    *amqp.Connection
	channel *amqp.Channel
}

func NewConnection(url string) (*RabbitMQ, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open a channel: %w", err)
	}

	// Enable publisher confirms for guaranteed message delivery
	if err := ch.Confirm(false); err != nil {
		log.Printf("Warning: failed to put channel in confirm mode: %v", err)
	}

	// 1. Declare Dead-Letter Exchange (DLX)
	err = ch.ExchangeDeclare(
		DLXExchange,
		"direct",
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to declare DLX: %w", err)
	}

	// 2. Declare Dead-Letter Queue (DLQ)
	_, err = ch.QueueDeclare(
		DLQQueue,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to declare DLQ: %w", err)
	}

	// Bind DLQ to DLX
	err = ch.QueueBind(
		DLQQueue,
		DLQRoutingKey,
		DLXExchange,
		false,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to bind DLQ to DLX: %w", err)
	}

	// 3. Declare Main Events Exchange
	err = ch.ExchangeDeclare(
		EventsExchange,
		"direct",
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to declare events exchange: %w", err)
	}

	// 4. Declare Main Queue with Dead-Letter Routing configuration
	queueArgs := amqp.Table{
		"x-dead-letter-exchange":    DLXExchange,
		"x-dead-letter-routing-key": DLQRoutingKey,
	}

	_, err = ch.QueueDeclare(
		TransferQueue,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		queueArgs,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to declare main queue: %w", err)
	}

	// Bind Main Queue to Events Exchange
	err = ch.QueueBind(
		TransferQueue,
		TransferRoutingKey,
		EventsExchange,
		false,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to bind main queue: %w", err)
	}

	log.Println("Connected to RabbitMQ with durable queues and DLX topology configured")
	return &RabbitMQ{conn: conn, channel: ch}, nil
}

// Publish sends a persistent message to the durable events exchange.
func (r *RabbitMQ) Publish(ctx context.Context, messageID string, body []byte) error {
	if r == nil || r.channel == nil {
		return fmt.Errorf("rabbitmq channel is not initialized")
	}

	confirmation, err := r.channel.PublishWithDeferredConfirmWithContext(ctx,
		EventsExchange,
		TransferRoutingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent, // Priority 3 & 4: Persistent messages (disk-backed)
			ContentType:  "application/json",
			MessageId:    messageID,
			Body:         body,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	// Wait for broker confirmation
	if confirmation != nil {
		if !confirmation.Wait() {
			return fmt.Errorf("failed to receive broker confirmation for message %s", messageID)
		}
	}

	return nil
}

// Consume starts consuming messages from the main durable queue with manual ACK mode.
func (r *RabbitMQ) Consume() (<-chan amqp.Delivery, error) {
	if r == nil || r.channel == nil {
		return nil, fmt.Errorf("rabbitmq channel is not initialized")
	}

	// Set QoS prefetch count to 10 for fair dispatch
	if err := r.channel.Qos(10, 0, false); err != nil {
		log.Printf("Warning: failed to set Qos prefetch: %v", err)
	}

	return r.channel.Consume(
		TransferQueue,
		"aurumledger_consumer", // consumer tag
		false,                  // auto-ack = false (Priority 4: Manual ACKs only!)
		false,                  // exclusive
		false,                  // no-local
		false,                  // no-wait
		nil,
	)
}

func (r *RabbitMQ) Close() {
	if r.channel != nil {
		_ = r.channel.Close()
	}
	if r.conn != nil {
		_ = r.conn.Close()
	}
}
