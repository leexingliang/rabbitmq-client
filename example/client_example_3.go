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
	queue := "test-queue"
	publish := mqclient.MQPublish{
		Exchange: "aha",
		Key:      queue,
	}

	client := mqclient.NewMQClient(base, make(chan interface{}, 100*10))
	go client.Consume(queue, func(delivery amqp.Delivery) error {
		fmt.Println(delivery.Body)
		return nil
	}, mqclient.WithMQConsume(mqclient.MQConsume{Tag: "hahah"}))

	go client.Publish(queue, mqclient.WithMQPublish(publish))
}
