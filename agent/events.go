package main

type UnsignedImageEvent struct {
	Message string `json:"msg"`
	Image   string `json:"image"`
	Reason  string `json:"reason"`
}

type NoSignatureConfigurationEvent struct {
	Message   string `json:"msg"`
	Container string `json:"container"`
}
