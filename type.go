package mqutil

// MQBase 消息队列配置
type MQBase struct {
	UserName string `toml:"username"`
	Password string `toml:"password"`
	URL      string `toml:"url"`
	VHost    string `toml:"vhost"`
}

// MQQueues 队列名字
type MQQueues struct {
	JoinRoom   string `toml:"joinroom"`
	FinishRoom string `toml:"finishroom"`
	WBData     string `toml:"whiteboard"`
}

type MQPublish struct {
	Exchange string `toml:"exchange"`
	Key      string `toml:"key"`
}

type MQConsume struct {
	Tag string `toml:"tag"`
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
	MQQos     MQQos
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

func WithMQQos(q MQQos) Option {
	return func(o *option) {
		o.MQQos = q
	}
}

var defaultOption option
