/*
Copyright 2020 IBM Corporation

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package messages

import (
	"fmt"
	"github.com/nats-io/nats.go"
	"k8s.io/klog"
)

type natsProvider struct {
	messageProviderDefinition *ProviderDefinition
	connection                *nats.Conn
	subscription              map[string]*nats.Subscription
}

func (provider *natsProvider) initialize(mpd *ProviderDefinition) error {
	provider.messageProviderDefinition = mpd
	nc, err := nats.Connect(mpd.URL)
	if err != nil {
		return err
	}

	provider.connection = nc
	provider.subscription = make(map[string]*nats.Subscription)
	return nil
}

func (provider *natsProvider) Subscribe(node *EventNode) error {
	if klog.V(6) {
		urlAndTopic := fmt.Sprintf("%s:%s", provider.messageProviderDefinition.URL, node.Topic)
		klog.Infof("Subscribing to NATS provider on %s", urlAndTopic)
	}
	sub, err := provider.connection.SubscribeSync(node.Topic)
	if err != nil {
		return err
	}

	provider.subscription[node.Name] = sub
	return nil
}

// Send an event to some eventSource.
func (provider *natsProvider) Send(node *EventNode, payload []byte, header interface{}) error {
	klog.Infof("natsProvider: Sending %s", string(payload))
	conn := provider.connection
	if err := conn.Publish(node.Topic, payload); err != nil {
		return err
	}

	// Perform a round trip to the server and return when it receives the internal reply.
	if err := conn.Flush(); err != nil {
		return err
	}

	return nil
}

// Receive an event from some eventDestination.
func (provider *natsProvider) Receive(node *EventNode) ([]byte, error) {
	sub, ok := provider.subscription[node.Name]

	if !ok {
		klog.Errorf("no subscription for eventSource '%s'. It should be defined and Subscribed to.", node.Name)
	}
	if klog.V(6) {
		klog.Infof("natsProvider: Looking for data from source %s and provider %s", node.Name, node.ProviderRef)
	}
	// timeout := provider.messageProviderDefinition.Timeout * time.Second
	timeout := provider.messageProviderDefinition.Timeout
	if klog.V(6) {
		klog.Infof("natsPovider.Receive timeout: %v", timeout)
	}
	msg, err := sub.NextMsg(timeout)

	if err != nil {
		return nil, err
	}

	return msg.Data, nil
}

// ListenAndServe listens for new eventDefinition on some eventSource and calls the ReceiverFunc on the message payload.
func (provider *natsProvider) ListenAndServe(node *EventNode, receiver ReceiverFunc) {
	urlAndTopic := fmt.Sprintf("%s:%s", provider.messageProviderDefinition.URL, node.Topic)
	if klog.V(5) {
		klog.Infof("natsProvider: Starting to listen for NATS eventDefinition from %s", urlAndTopic)
	}

	msgChan := make(chan *nats.Msg)
	sub, err := provider.connection.ChanSubscribe(node.Topic, msgChan)

	if err != nil {
		klog.Errorf("unable to set up listener for NATS eventDefinition for %s", urlAndTopic)
	}

	for msg := range msgChan {
		if klog.V(8) {
			klog.Infof("Received message on %s: %s", urlAndTopic, msg.Data)
		}
		receiver(msg.Data)
	}

	sub.Unsubscribe()
	sub.Drain()
}

func newNATSProvider(mpd *ProviderDefinition) (*natsProvider, error) {
	provider := new(natsProvider)
	if err := provider.initialize(mpd); err != nil {
		return nil, err
	}

	return provider, nil
}
