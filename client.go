package mqclient

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"github.com/streadway/amqp"
)

// ErrShutdown 客户端连接断开
var ErrShutdown = errors.New("rabbitmq client is shutdown")

// ErrNotSend 消息未发送或者发送失败
var ErrNotSend = errors.New("message not send")

// ErrQueueExist queue 已经存在绑定关系
var ErrQueueExist = errors.New("queue has binding sending channel")

// ErrClose 客户端已关闭
var ErrClose = errors.New("closed")

// ErrQueueNotBindChannel queue 没有 发送channel 的绑定关系
var ErrQueueNotBindChannel = errors.New("queue not bind send channel")

// ConsumeCallback consume 回调处理
type ConsumeCallback func(amqp.Delivery) error

// Client rabbitmq 客户端
type Client struct {
	url           string
	conn          *amqp.Connection
	channel       *amqp.Channel
	connNotify    chan *amqp.Error
	channelNotify chan *amqp.Error

	status int32          // 当前客户端状态， 正常还是断线
	wg     sync.WaitGroup // 如果连接断掉了， consume 需要阻塞等待

	ctx        context.Context // 通知退出
	cancelFunc context.CancelFunc

	sendChan map[string]chan interface{} // 数据发送通道, queue->channel
	lock     sync.RWMutex
}

// NewMQClient 创建 rabbitmq 客户端
func NewMQClient(base MQBase) *Client {

	client := Client{
		sendChan: make(map[string]chan interface{}),
	}
	client.ctx, client.cancelFunc = context.WithCancel(context.Background())
	client.url = BuildURL(base)

	if err := client.connect(); err != nil {
		panic(err)
	}

	go client.reConnect()

	return &client
}

// BuildURL 构建 rabbitmq url
func BuildURL(b MQBase) string {
	return fmt.Sprintf("amqp://%s:%s@%s/%s", b.UserName, b.Password, b.URL, b.VHost)
}

// BindChannel 消息队列绑定消息发送的 channel
func (c *Client) BindChannel(queue string, sendChan chan interface{}) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	if _, ok := c.sendChan[queue]; ok {
		return ErrQueueExist
	}
	c.sendChan[queue] = sendChan
	return nil
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

	c.connNotify = c.conn.NotifyClose(make(chan *amqp.Error, 1))
	c.channelNotify = c.channel.NotifyClose(make(chan *amqp.Error, 1))

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
		case <-c.ctx.Done():
			return
		}

		c.wg.Add(1)
		atomic.StoreInt32(&c.status, int32(shutdown))

		if err := c.conn.Close(); err != nil {
			log.Println("rabbitmq - channel close failed: ", err)
		}

		c.cleanNotifyChannel()
		for {
			select {
			case <-c.ctx.Done():
				c.wg.Done()
				return
			default:
				if err := c.connect(); err != nil {
					log.Println("rabbitmq - fail connect: ", err)

					// sleep 5s
					time.Sleep(time.Second * 5)
					continue
				}
			}
			// reconnect success
			c.wg.Done()
			atomic.StoreInt32(&c.status, int32(normal))
			break
		}
	}
}

func (c *Client) cleanNotifyChannel() {
	// 清空 notify channel，否则死连接不会释放
	for err := range c.channelNotify {
		println(err)
	}
	for err := range c.connNotify {
		println(err)
	}
}

func (c *Client) isClosed() bool {
	if atomic.LoadInt32(&c.status) == int32(closed) {
		return true
	}
	return false
}

// Consume ...
func (c *Client) Consume(queue string, callback ConsumeCallback, options ...Option) error {
	option := defaultOption
	for _, opt := range options {
		opt(&option)
	}

again:
	c.wg.Wait()
	if c.isClosed() {
		return ErrClose
	}
	if err := c.declareQueue(queue); err != nil {
		log.Println(err.Error())
		goto again
	}

	// qos
	if err := c.channel.Qos(option.MQQos.Num, 0, false); err != nil {
		// todo
	}

reconsume:
	if err := c.consume(queue, callback, option); err != nil {
		// 检查一下错误码
		if err == ErrQueueNotBindChannel {
			return errors.Wrapf(err, "queue: %s", queue)
		} else if err == ErrClose {
			return ErrClose
		}
		log.Println(err.Error())
	}

	time.Sleep(100 * time.Millisecond)
	goto reconsume
}

func (c *Client) consume(queue string, callback ConsumeCallback, option option) error {
	if c.isClosed() {
		return ErrClose
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
		return errors.Wrap(err, "consume: channel consume error")
	}
	for {
		select {
		case <-c.ctx.Done():
			return ErrClose
		case msg, ok := <-msgchan:
			if !ok {
				return errors.New("consume channel is closed")
			}
			// 回调处理失败, 重入队列
			if err := callback(msg); err != nil {
				c.SendMessageNonBlock(queue, msg.Body)
			}
		}
	}
}

// Publish ...
// key 和 exchange 如果为空， 绑定到默认的 exchange 上
func (c *Client) Publish(queue string, options ...Option) error {
	option := defaultOption
	for _, opt := range options {
		opt(&option)
	}

again:
	c.wg.Wait()
	if c.isClosed() {
		return ErrClose
	}

	if err := c.declareQueue(queue); err != nil {
		log.Println(err.Error())
		goto again
	}

	if err := c.bindExchange(queue, option); err != nil {
		log.Println(err.Error())
		goto again
	}

republish:
	if err := c.publish(queue, option); err != nil {
		// 检查一下错误码
		if err == ErrQueueNotBindChannel {
			return errors.Wrapf(err, "queue: %s", queue)
		} else if err == ErrClose {
			return ErrClose
		}
		log.Println(err.Error())
	}

	time.Sleep(100 * time.Millisecond)
	goto republish
}

func (c *Client) declareQueue(queue string) error {
	if _, err := c.channel.QueueDeclare(
		queue, // name
		true,  // durable
		false, // delete when usused
		false, // exclusive
		true,  // no-wait
		nil,   // arguments
	); err != nil {
		return errors.Wrap(err, "queue declare error")
	}
	return nil
}

func (c *Client) bindExchange(queue string, option option) error {
	// bind exchange
	if option.MQExchange.Exchange != defaultOption.MQExchange.Exchange {
		// exchange declare
		if err := c.channel.ExchangeDeclare(
			option.MQExchange.Exchange,
			option.MQExchange.Type,
			true,
			false,
			false,
			true,
			nil,
		); err != nil {
			return errors.Wrap(err, "publish: queue bind exchange error")
		}
		// 将 queue 通过 key 绑定到 exchange 上
		if err := c.channel.QueueBind(
			queue,
			option.MQPublish.Key,
			option.MQExchange.Exchange,
			true,
			nil,
		); err != nil {
			return errors.Wrap(err, "publish: queue bind exchange error")
		}
	}
	return nil
}

// 返回值表示客户端是否已经 close
func (c *Client) publish(queue string, option option) error {
	if c.isClosed() {
		return ErrClose
	}

	sendChan, err := c.queueChannel(queue)
	if err != nil {
		return err
	}

	for {
		select {
		case <-c.ctx.Done():
			return ErrClose
		case msg := <-sendChan:
			if err := c.channel.Publish(
				option.MQExchange.Exchange, // exchange
				option.MQPublish.Key,       // routing key
				true,                       // mandatory
				false,                      // immediate
				amqp.Publishing{
					ContentType: "application/json",
					Body:        msg.([]byte),
				},
			); err != nil {
				c.SendMessageNonBlock(queue, msg.([]byte))
				return err
			}
		}
	}
}

// 找到和 queue 绑定的发送队列(channel)
func (c *Client) queueChannel(queue string) (chan interface{}, error) {
	c.lock.RLock()
	defer c.lock.RUnlock()
	ch, ok := c.sendChan[queue]
	if !ok {
		return nil, ErrQueueNotBindChannel
	}
	return ch, nil
}

func (c *Client) SendMessage(queue string, msg []byte) error {
	sendChan, err := c.queueChannel(queue)
	if err != nil {
		return err
	}

	sendChan <- msg

	return nil
}

func (c *Client) SendMessageNonBlock(queue string, msg []byte) error {
	sendChan, err := c.queueChannel(queue)
	if err != nil {
		return err
	}

	select {
	case sendChan <- msg:
	default:
		return ErrNotSend
	}
	return nil
}

func (c *Client) Close() {
	c.cancelFunc()
	atomic.StoreInt32(&c.status, int32(closed))
	c.conn.Close()
}
