package mqutil

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/streadway/amqp"
)

var ErrShutdown = errors.New("rabbitmq client is shutdwon")
var ErrNotSend = errors.New("message not send")

type Client struct {
	base MQBase

	url           string
	conn          *amqp.Connection
	channel       *amqp.Channel
	connNotify    chan *amqp.Error
	channelNotify chan *amqp.Error

	status int32          // 当前客户端状态， 正常还是断线
	wg     sync.WaitGroup // 如果连接断掉了， consume 需要阻塞等待

	quit     chan interface{} // 通知退出
	dataChan chan interface{} // 数据通道
}

// NewMQClient 创建 rabbitmq 客户端
func NewMQClient(
	b MQBase,
	ch chan interface{},
) *Client {
	client := Client{
		base:     b,
		dataChan: ch,
		quit:     make(chan interface{}, 3),
	}

	client.url = BuildURL(client.base)

	if err := client.connect(); err != nil {
		panic(err)
	}

	client.connNotify = client.conn.NotifyClose(make(chan *amqp.Error, 1))
	client.channelNotify = client.channel.NotifyClose(make(chan *amqp.Error, 1))

	go client.reConnect()

	return &client
}

// BuildURL 构建 rabbitmq url
func BuildURL(b MQBase) string {
	return fmt.Sprintf("amqp://%s:%s@%s/%s", b.UserName, b.Password, b.URL, b.VHost)
}

// 连接 rabbitmq
func (c *Client) connect() error {
	var err error
	if c.conn, err = amqp.Dial(c.url); err != nil {
		return err
	}

	if c.channel, err = c.conn.Channel(); err != nil {
		c.conn.Close()
		return err
	}
	return nil
}

// 重连逻辑
func (c *Client) reConnect() {
	for {
		select {
		case err := <-c.connNotify:
			if err != nil {
				log.Println("rabbitmq - connection NotifyClose: ", err)
			}
		case err := <-c.channelNotify:
			if err != nil {
				log.Println("rabbitmq - channel NotifyClose: ", err)
			}
		case <-c.quit:
			return
		}

		c.wg.Add(1)
		atomic.StoreInt32(&c.status, int32(shutdown))

		if err := c.conn.Close(); err != nil {
			log.Println("rabbitmq - channel close failed: ", err)
		}

		// 清空 notify channel，否则死连接不会释放
		for err := range c.channelNotify {
			println(err)
		}
		for err := range c.connNotify {
			println(err)
		}

		for {
			select {
			case <-c.quit:
				return
			default:
				if err := c.connect(); err != nil {
					log.Println("rabbitmq - fail connect: ", err)

					// sleep 5s
					time.Sleep(time.Second * 5)
					continue
				}
				c.wg.Done()
				atomic.StoreInt32(&c.status, int32(normal))

				c.connNotify = c.conn.NotifyClose(make(chan *amqp.Error, 1))
				c.channelNotify = c.channel.NotifyClose(make(chan *amqp.Error, 1))
				break
			}
		}
	}
}

// Consume ...
func (c *Client) Consume(queue string, callback func(amqp.Delivery), options ...Option) error {
	if atomic.LoadInt32(&c.status) != int32(normal) {
		return ErrShutdown
	}

	option := defaultOption
	for _, opt := range options {
		opt(&option)
	}
again:
	c.wg.Wait()
	if _, err := c.channel.QueueDeclare(
		queue, // name
		true,  // durable
		false, // delete when usused
		false, // exclusive
		true,  // no-wait
		nil,   // arguments
	); err != nil {
		log.Printf("consume: queue delare error: %s\n", err.Error())
		goto again
	}

	// qos
	if err := c.channel.Qos(option.MQQos.Num, 0, false); err != nil {
		// todo
	}

	// consume
	msgchan, err := c.channel.Consume(
		queue,
		option.MQConsume.Tag,
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("consume: channel consume error: %s\n", err.Error())
		goto again
	}

	for {
		select {
		case msg, ok := <-msgchan:
			if !ok {
				status := atomic.LoadInt32(&c.status)
				if status == int32(shutdown) {
					goto again
				} else if status == int32(closed) {
					break
				}
				// 休眠 500 ms
				time.Sleep(500 * time.Millisecond)
				continue
			}
			callback(msg)
		case <-c.quit:
			break
		}
	}
}

// Publish ...
// key 和 exchange 如果为空， 绑定到默认的 exchange 上
func (c *Client) Publish(queue string, options ...Option) error {
	if atomic.LoadInt32(&c.status) != int32(normal) {
		return ErrShutdown
	}

	option := defaultOption
	for _, opt := range options {
		opt(&option)
	}

again:
	c.wg.Wait()
	if _, err := c.channel.QueueDeclare(
		queue, // name
		true,  // durable
		false, // delete when usused
		false, // exclusive
		true,  // no-wait
		nil,   // arguments
	); err != nil {
		log.Printf("publish: queue declare error: %s\n", err.Error())
		goto again
	}
	// bind exchange
	if err := c.channel.QueueBind(
		queue,
		option.MQPublish.Key,
		option.MQPublish.Exchange,
		true,
		nil,
	); err != nil {
		log.Printf("publish: queue bind exchange error: %s\n", err.Error())
		goto again
	}

	for {
		select {
		case msg := <-c.dataChan:
			if err := c.channel.Publish(
				option.MQPublish.Exchange, // exchange
				option.MQPublish.Key,      // routing key
				true,                      // mandatory
				false,                     // immediate
				amqp.Publishing{
					ContentType: "application/json",
					Body:        msg.([]byte),
				},
			); err != nil {
				c.dataChan <- msg
				log.Printf("send msg error: %s\n", err.Error())
				status := atomic.LoadInt32(&c.status)
				if status == int32(shutdown) {
					goto again
				} else if status == int32(closed) {
					break
				}
				// 休眠 500 ms
				time.Sleep(500 * time.Millisecond)
				continue
			}
		case <-c.quit:
			break
		}
	}
	return nil
}

func (c *Client) SendMessage(msg []byte) {
	c.dataChan <- msg
}

func (c *Client) SendMessageNonBlock(msg []byte) error {
	select {
	case c.dataChan <- msg:
	default:
		return ErrNotSend
	}
	return nil
}

func (c *Client) Close() {
	c.quit <- true
	c.quit <- true
	c.quit <- true
	atomic.StoreInt32(&c.status, int32(closed))
	c.conn.Close()
}
