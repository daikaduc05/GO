package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/sirupsen/logrus"
)

// TransportError represents transport-specific errors
type TransportError struct {
	Message string
	Err     error
}

func (te *TransportError) Error() string {
	if te.Err != nil {
		return fmt.Sprintf("%s: %v", te.Message, te.Err)
	}
	return te.Message
}

// MessageHandler is a function type for handling incoming messages
type MessageHandler func([]byte)

// Transport is the interface for data transport implementations
type Transport interface {
	AttachChannel(*webrtc.DataChannel)
	OnMessage(MessageHandler)
	SetDefaultMessageHandler()
	SendText(string) error
	SendJSON(interface{}) error
	SendBytes([]byte) error
	Close() error
}

// BaseTransport provides common transport functionality
type BaseTransport struct {
	channel        *webrtc.DataChannel
	messageHandler MessageHandler
	outboxQueue    chan []byte
	senderCtx      context.Context
	senderCancel   context.CancelFunc
	stdinCtx       context.Context
	stdinCancel    context.CancelFunc
	maxQueueSize   int
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	logger         *logrus.Logger
	mu             sync.RWMutex
}

// NewBaseTransport creates a new base transport
func NewBaseTransport(maxQueueSize int) *BaseTransport {
	ctx, cancel := context.WithCancel(context.Background())

	return &BaseTransport{
		outboxQueue:    make(chan []byte, maxQueueSize),
		maxQueueSize:   maxQueueSize,
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
		logger:         logrus.New(),
	}
}

// AttachChannel attaches a WebRTC data channel to the transport
func (bt *BaseTransport) AttachChannel(channel *webrtc.DataChannel) {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	if bt.channel != nil {
		bt.logger.Warning("Channel already attached, replacing")
	}

	bt.channel = channel

	// Set up channel event handlers
	channel.OnOpen(func() {
		bt.logger.Info("Data channel opened")
		fmt.Println("Chat session started. Type 'quit' to exit.")
		bt.startSender()
		bt.startStdinReader()
	})

	channel.OnClose(func() {
		bt.logger.Info("Data channel closed")
		bt.stopSender()
		bt.stopStdinReader()
	})

	channel.OnMessage(func(msg webrtc.DataChannelMessage) {
		bt.handleChannelMessage(msg.Data)
	})

	bt.logger.WithField("label", channel.Label()).Info("Attached channel")
	bt.logger.WithField("state", channel.ReadyState().String()).Info("Channel ready state")

	// If channel is already open, trigger the open event manually
	if channel.ReadyState() == webrtc.DataChannelStateOpen {
		bt.logger.Info("Channel already open, triggering open event manually")
		bt.startSender()
		bt.startStdinReader()
	}
}

// OnMessage registers a callback for incoming messages
func (bt *BaseTransport) OnMessage(handler MessageHandler) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.messageHandler = handler
	bt.logger.Debug("Message callback registered")
}

// SetDefaultMessageHandler sets up default message handler that logs and prints to console
func (bt *BaseTransport) SetDefaultMessageHandler() {
	handler := func(data []byte) {
		text := string(data)
		fmt.Printf("> %s\n", text) // Print to console
	}

	bt.OnMessage(handler)
	bt.logger.Debug("Default message handler registered")
}

// handleChannelMessage handles incoming channel messages
func (bt *BaseTransport) handleChannelMessage(data []byte) {
	bt.mu.RLock()
	handler := bt.messageHandler
	bt.mu.RUnlock()

	if handler != nil {
		handler(data)
	} else {
		bt.logger.Debug("Message received but no callback registered")
	}
}

// startSender starts background task to send queued messages
func (bt *BaseTransport) startSender() {
	if bt.senderCtx != nil {
		bt.senderCancel()
	}

	bt.senderCtx, bt.senderCancel = context.WithCancel(bt.shutdownCtx)

	go func() {
		defer bt.senderCancel()

		for {
			select {
			case <-bt.senderCtx.Done():
				bt.logger.Debug("Sender loop cancelled")
				return
			case message := <-bt.outboxQueue:
				if err := bt.sendRaw(message); err != nil {
					bt.logger.WithError(err).Error("Error in sender loop")
				}
			case <-time.After(time.Second):
				// Timeout to check if channel is still open
				bt.mu.RLock()
				channel := bt.channel
				bt.mu.RUnlock()

				if channel == nil || channel.ReadyState() != webrtc.DataChannelStateOpen {
					bt.logger.Debug("Channel closed, stopping sender")
					return
				}
			}
		}
	}()

	bt.logger.Info("Started message sender task")
}

// stopSender stops background sender task
func (bt *BaseTransport) stopSender() {
	if bt.senderCancel != nil {
		bt.senderCancel()
		bt.logger.Debug("Stopped message sender task")
	}
}

// startStdinReader starts background task to read from stdin
func (bt *BaseTransport) startStdinReader() {
	if bt.stdinCtx != nil {
		bt.stdinCancel()
	}

	bt.stdinCtx, bt.stdinCancel = context.WithCancel(bt.shutdownCtx)

	go func() {
		defer bt.stdinCancel()

		scanner := bufio.NewScanner(os.Stdin)
		bt.logger.Info("Stdin reader loop started")

		for {
			select {
			case <-bt.stdinCtx.Done():
				bt.logger.Debug("Stdin reader loop cancelled")
				return
			default:
				// Read from stdin
				if scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "" {
						continue
					}

					if strings.ToLower(line) == "quit" {
						bt.logger.Info("User requested quit")
						bt.shutdownCancel()
						return
					}

					// Send the text
					if err := bt.queueMessage([]byte(line)); err != nil {
						bt.logger.WithError(err).Error("Failed to send text")
					}
				} else {
					if err := scanner.Err(); err != nil {
						bt.logger.WithError(err).Error("Error reading from stdin")
					}
					return
				}
			}
		}
	}()

	bt.logger.Info("Started stdin reader task")
}

// stopStdinReader stops background stdin reader task
func (bt *BaseTransport) stopStdinReader() {
	if bt.stdinCancel != nil {
		bt.stdinCancel()
		bt.logger.Debug("Stopped stdin reader task")
	}
}

// sendRaw sends raw bytes over data channel
func (bt *BaseTransport) sendRaw(data []byte) error {
	bt.mu.RLock()
	channel := bt.channel
	bt.mu.RUnlock()

	if channel == nil || channel.ReadyState() != webrtc.DataChannelStateOpen {
		return &TransportError{Message: "Data channel not available or not open"}
	}

	return channel.Send(data)
}

// queueMessage queues a message for sending
func (bt *BaseTransport) queueMessage(data []byte) error {
	select {
	case bt.outboxQueue <- data:
		return nil
	default:
		// Queue is full, try to remove oldest message and add new one
		select {
		case <-bt.outboxQueue:
			select {
			case bt.outboxQueue <- data:
				bt.logger.Warning("Dropped oldest message to make room")
				return nil
			default:
				return &TransportError{Message: "Outbox queue full, cannot queue message"}
			}
		default:
			return &TransportError{Message: "Outbox queue full, cannot queue message"}
		}
	}
}

// Close closes transport and cleans up resources
func (bt *BaseTransport) Close() error {
	bt.logger.Info("Closing transport")

	bt.shutdownCancel()
	bt.stopSender()
	bt.stopStdinReader()

	bt.mu.Lock()
	bt.channel = nil
	bt.messageHandler = nil
	bt.mu.Unlock()

	close(bt.outboxQueue)

	bt.logger.Info("Transport closed")
	return nil
}

// ChatTransport implements transport for UTF-8 text messages
type ChatTransport struct {
	*BaseTransport
}

// NewChatTransport creates a new chat transport
func NewChatTransport(maxQueueSize int) *ChatTransport {
	return &ChatTransport{
		BaseTransport: NewBaseTransport(maxQueueSize),
	}
}

// SendText sends UTF-8 text message
func (ct *ChatTransport) SendText(text string) error {
	data := []byte(text)
	return ct.queueMessage(data)
}

// SendJSON sends JSON object as text
func (ct *ChatTransport) SendJSON(obj interface{}) error {
	jsonStr, err := json.Marshal(obj)
	if err != nil {
		return &TransportError{Message: "Failed to serialize JSON", Err: err}
	}
	return ct.SendText(string(jsonStr))
}

// SendBytes sends raw bytes (will be treated as UTF-8)
func (ct *ChatTransport) SendBytes(data []byte) error {
	// Validate that bytes can be decoded as UTF-8
	if !isValidUTF8(data) {
		return &TransportError{Message: "Bytes cannot be decoded as UTF-8"}
	}
	return ct.queueMessage(data)
}

// JSONTransport implements transport for JSON messages
type JSONTransport struct {
	*BaseTransport
}

// NewJSONTransport creates a new JSON transport
func NewJSONTransport(maxQueueSize int) *JSONTransport {
	return &JSONTransport{
		BaseTransport: NewBaseTransport(maxQueueSize),
	}
}

// SendText sends text as JSON string
func (jt *JSONTransport) SendText(text string) error {
	// Try to parse as JSON first
	var obj interface{}
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return &TransportError{Message: "Text is not valid JSON", Err: err}
	}

	data := []byte(text)
	return jt.queueMessage(data)
}

// SendJSON sends JSON object
func (jt *JSONTransport) SendJSON(obj interface{}) error {
	jsonStr, err := json.Marshal(obj)
	if err != nil {
		return &TransportError{Message: "Failed to serialize JSON", Err: err}
	}
	return jt.queueMessage(jsonStr)
}

// SendBytes sends bytes (must be valid JSON)
func (jt *JSONTransport) SendBytes(data []byte) error {
	// Validate that bytes can be decoded as UTF-8 and parsed as JSON
	var obj interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return &TransportError{Message: "Bytes are not valid JSON", Err: err}
	}
	return jt.queueMessage(data)
}

// BytesTransport implements transport for raw binary data
type BytesTransport struct {
	*BaseTransport
}

// NewBytesTransport creates a new bytes transport
func NewBytesTransport(maxQueueSize int) *BytesTransport {
	return &BytesTransport{
		BaseTransport: NewBaseTransport(maxQueueSize),
	}
}

// SendText sends text as UTF-8 bytes
func (bt *BytesTransport) SendText(text string) error {
	data := []byte(text)
	return bt.queueMessage(data)
}

// SendJSON sends JSON object as UTF-8 bytes
func (bt *BytesTransport) SendJSON(obj interface{}) error {
	jsonStr, err := json.Marshal(obj)
	if err != nil {
		return &TransportError{Message: "Failed to serialize JSON", Err: err}
	}
	return bt.queueMessage(jsonStr)
}

// SendBytes sends raw bytes
func (bt *BytesTransport) SendBytes(data []byte) error {
	return bt.queueMessage(data)
}

// CreateTransport creates a transport instance based on mode
func CreateTransport(mode string) Transport {
	maxQueueSize := 1000

	switch mode {
	case "chat":
		return NewChatTransportWrapper(maxQueueSize)
	case "json":
		return NewJSONTransport(maxQueueSize)
	case "bytes":
		return NewBytesTransport(maxQueueSize)
	default:
		// Default to chat transport
		return NewChatTransportWrapper(maxQueueSize)
	}
}

// ChatTransportWrapper is a wrapper around ChatTransport that disables automatic stdin reading
type ChatTransportWrapper struct {
	*ChatTransport
}

// NewChatTransportWrapper creates a new chat transport wrapper
func NewChatTransportWrapper(maxQueueSize int) *ChatTransportWrapper {
	chat := NewChatTransport(maxQueueSize)
	return &ChatTransportWrapper{ChatTransport: chat}
}

// AttachChannel overrides the base implementation to disable automatic stdin reading
func (ctw *ChatTransportWrapper) AttachChannel(channel *webrtc.DataChannel) {
	ctw.mu.Lock()
	defer ctw.mu.Unlock()

	if ctw.channel != nil {
		ctw.logger.Warning("Channel already attached, replacing")
	}

	ctw.channel = channel

	// Set up channel event handlers (but don't start stdin reader)
	channel.OnOpen(func() {
		ctw.logger.Info("Data channel opened (chat mode)")
		ctw.startSender() // Only start sender, not stdin reader
	})

	channel.OnClose(func() {
		ctw.logger.Info("Data channel closed (chat mode)")
		ctw.stopSender()
		ctw.stopStdinReader()
	})

	channel.OnMessage(func(msg webrtc.DataChannelMessage) {
		ctw.handleChannelMessage(msg.Data)
	})

	ctw.logger.WithField("label", channel.Label()).Info("Attached channel (chat mode)")
	ctw.logger.WithField("state", channel.ReadyState().String()).Info("Channel ready state")

	// If channel is already open, trigger the open event manually
	if channel.ReadyState() == webrtc.DataChannelStateOpen {
		ctw.logger.Info("Channel already open, triggering open event manually (chat mode)")
		ctw.startSender() // Only start sender, not stdin reader
	}
}

// isValidUTF8 checks if data is valid UTF-8
func isValidUTF8(data []byte) bool {
	return len(data) == len(string(data))
}
