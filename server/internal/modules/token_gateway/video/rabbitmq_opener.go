package video

import (
	"context"
	"net"
	"net/url"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

// NewTaskConnectionOpener冻结服务端RabbitMQ地址；闭包不实现格式化接口，也不把含密码URL写入错误。
func NewTaskConnectionOpener(raw string) (TaskConnectionOpener, error) {
	parsed, err := url.Parse(raw)
	password, hasPassword := "", false
	if parsed.User != nil {
		password, hasPassword = parsed.User.Password()
	}
	if err != nil || (parsed.Scheme != "amqp" && parsed.Scheme != "amqps") || parsed.Hostname() == "" || parsed.User == nil || parsed.User.Username() == "" || !hasPassword || password == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && !strings.HasPrefix(parsed.Path, "/")) {
		return nil, ErrTaskBrokerUnavailable
	}
	return func(ctx context.Context) (*amqp.Connection, error) {
		if ctx == nil || ctx.Err() != nil {
			return nil, ErrTaskBrokerUnavailable
		}
		connection, dialErr := amqp.DialConfig(raw, amqp.Config{Dial: func(network, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		}})
		if dialErr != nil {
			return nil, ErrTaskBrokerUnavailable
		}
		return connection, nil
	}, nil
}
