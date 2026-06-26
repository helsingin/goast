// Package greeter provides greeting functionality.
package greeter

import "fmt"

// DefaultGreeting is the default greeting message.
const DefaultGreeting = "Hello"

// GreetCount tracks how many greetings have been made.
var GreetCount int

// Greeter generates personalized greetings.
type Greeter struct {
	// Prefix is prepended to all greetings.
	Prefix string `json:"prefix"`
	// Suffix is appended to all greetings.
	Suffix string `json:"suffix,omitempty"`
}

// Greet returns a greeting for the given name.
func (g *Greeter) Greet(name string) string {
	GreetCount++
	return fmt.Sprintf("%s%s %s%s", g.Prefix, DefaultGreeting, name, g.Suffix)
}

// Reset clears the greeting count.
func (g *Greeter) Reset() {
	GreetCount = 0
}

// Speaker is something that can speak a message.
type Speaker interface {
	// Speak says the given message.
	Speak(msg string) error
	// Volume returns the current volume level.
	Volume() int
}

// LoudSpeaker is a speaker that yells.
type LoudSpeaker struct {
	// Level is the volume level.
	Level int
}

// Speak says the given message loudly.
func (ls *LoudSpeaker) Speak(s string) error {
	fmt.Println(s)
	return nil
}

// Volume returns the current volume level.
func (ls *LoudSpeaker) Volume() int {
	return ls.Level
}

// GRPCConfig holds gRPC server settings.
type GRPCConfig struct {
	// Host is the gRPC listen address.
	Host string `yaml:"host"`
	// Port is the gRPC listen port.
	Port int `yaml:"port"`
}

// HTTPConfig holds HTTP server settings.
type HTTPConfig struct {
	// Port is the HTTP listen port.
	Port int `yaml:"port"`
	// ReadTimeoutMS is the read timeout in milliseconds.
	ReadTimeoutMS int `yaml:"read_timeout_ms"`
}

// GreeterConfig holds configuration for the greeter service.
type GreeterConfig struct {
	// GRPC holds gRPC server config.
	GRPC GRPCConfig `yaml:"grpc"`
	// HTTP holds HTTP server config.
	HTTP HTTPConfig `yaml:"http"`
	// Verbose enables verbose logging.
	Verbose bool `yaml:"verbose"`
}

// DefaultGreeterConfig returns the default configuration.
func DefaultGreeterConfig() GreeterConfig {
	return GreeterConfig{
		GRPC: GRPCConfig{
			Host: "127.0.0.1",
			Port: 50051,
		},
		HTTP: HTTPConfig{
			Port:          8080,
			ReadTimeoutMS: 5000,
		},
		Verbose: false,
	}
}

// HappySpeaker embeds LoudSpeaker to demonstrate promoted methods from an
// embedded struct field. It satisfies Speaker via the embedded methods.
type HappySpeaker struct {
	LoudSpeaker
	// Mood describes the speaker's current mood.
	Mood string
}

// AudioDevice is a Speaker that can also be muted. Embedding Speaker should
// contribute Speak/Volume to AudioDevice's method set.
type AudioDevice interface {
	Speaker
	// Mute silences the device.
	Mute() error
}

// MuteLoudSpeaker embeds LoudSpeaker and adds Mute to satisfy AudioDevice.
type MuteLoudSpeaker struct {
	LoudSpeaker
}

// Mute silences the speaker.
func (m *MuteLoudSpeaker) Mute() error {
	return nil
}

// NewGreeter creates a new Greeter with the given prefix.
func NewGreeter(prefix string) *Greeter {
	return &Greeter{Prefix: prefix}
}

// FormatGreeting formats a greeting string. It handles edge cases
// like empty names by returning just the greeting.
func FormatGreeting(greeting, name string) string {
	if name == "" {
		return greeting
	}
	return fmt.Sprintf("%s, %s!", greeting, name)
}
