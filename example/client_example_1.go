package main

import (
	mqclient "github.com/leexingliang/rabbitmq-client"
)

func main() {

	base := mqclient.MQBase{
		UserName: "admin",
		Password: "admin",
		URL:      "localhost:5437",
		VHost:    "test",
	}
	queue := "test-queue"

	client := mqclient.NewMQClient(base)
	client.BindChannel(queue, make(chan interface{}, 100*1000))
	go client.Publish(queue,
		mqclient.WithMQRouting(mqclient.MQRouting{Key: queue}))
}
