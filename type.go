package mqclient

// MQBase 消息队列配置
type MQBase struct {
	UserName string
	Password string
	URL      string
	VHost    string
}

type MQPublish struct {
	Key string
}

type MQExchange struct {
	Exchange string
	Type     string
}

type MQConsume struct {
	Tag string
}

type MQQos struct {
	Num int
}

// mqstatus 客户端连接状态
type mqstatus int32

const (
	normal   mqstatus = 0
	shutdown mqstatus = 1
	closed   mqstatus = 2
)

type Option func(*option)

type option struct {
	MQPublish MQPublish
	MQConsume MQConsume

	MQExchange MQExchange
	MQQos      MQQos
}

func WithMQPublish(p MQPublish) Option {
	return func(o *option) {
		o.MQPublish = p
	}
}

func WithMQConsume(c MQConsume) Option {
	return func(o *option) {
		o.MQConsume = c
	}
}

func WithMQExchange(e MQExchange) Option {
	return func(o *option) {
		o.MQExchange = e
	}
}

func WithMQQos(q MQQos) Option {
	return func(o *option) {
		o.MQQos = q
	}
}

var defaultOption option
