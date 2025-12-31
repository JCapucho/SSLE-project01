package events

import "time"

const (
	UNSIGNED_IMAGE_EVENT_CODE       uint = 1
	NO_SIGNTAURE_CONFIGURATION_CODE uint = 1
)

type baseEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Code      uint      `json:"code"`
	Message   string    `json:"msg"`
}

func newBaseEvent(code uint, msg string) baseEvent {
	return baseEvent{
		Timestamp: time.Now(),
		Code:      code,
		Message:   msg,
	}
}

type unsignedImageEvent struct {
	baseEvent
	Image  string `json:"image"`
	Reason string `json:"reason"`
}

func NewUnsignedImageEvent(image string, reason string) any {
	baseEvent := newBaseEvent(UNSIGNED_IMAGE_EVENT_CODE, "Unsigned image detected")
	return unsignedImageEvent{
		baseEvent: baseEvent,
		Image:     image,
		Reason:    reason,
	}
}

type noSignatureConfigurationEvent struct {
	baseEvent
	Container string `json:"container"`
}

func NewNoSignatureConfigurationEvent(container string) any {
	baseEvent := newBaseEvent(NO_SIGNTAURE_CONFIGURATION_CODE, "Container does not have signature verification configured")
	return noSignatureConfigurationEvent{
		baseEvent: baseEvent,
		Container: container,
	}
}
