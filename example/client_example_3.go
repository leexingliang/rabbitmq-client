package main

import (
	"fmt"

	mqclient "github.com/leexingliang/rabbitmq-client"
	"github.com/streadway/amqp"
)

func main() {
	base := mqclient.MQBase{
		UserName: "admin",
		Password: "admin",
		URL:      "localhost:5437",
		VHost:    "test",
	}
	key := "test"
	queue := "test-queue"
	routingkey := mqclient.MQRouting{
		Key: key,
	}
	exchange := mqclient.MQExchange{
		Exchange: "test-exchange",
		Type:     "direct",
	}

	client := mqclient.NewMQClient(base)
	client.BindChannel(queue, make(chan interface{}, 100*10))
	go client.Consume(queue, func(delivery amqp.Delivery) error {
		fmt.Println(delivery.Body)
		return nil
	}, mqclient.WithMQConsume(mqclient.MQConsume{Tag: "hahah"}),
		mqclient.WithMQRouting(routingkey), mqclient.WithMQExchange(exchange))

	go client.Publish(queue,
		mqclient.WithMQRouting(routingkey),
		mqclient.WithMQExchange(exchange))
}
