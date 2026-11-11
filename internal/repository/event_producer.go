package repository

import (
	"context"
	"encoding/json"

	"aurumledger/internal/domain"
	"aurumledger/pkg/rabbitmq"
)

type eventProducer struct {
	mq *rabbitmq.RabbitMQ
}

func NewEventProducer(mq *rabbitmq.RabbitMQ) domain.EventProducer {
	return &eventProducer{mq: mq}
}

func (p *eventProducer) PublishTransferEvent(ctx context.Context, event domain.TransferEvent) error {
	if p.mq == nil {
		return nil
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return p.mq.Publish(ctx, event.EventID.String(), body)
}
