package main

import (
	"fmt"
	"os"
	"os/signal"
	"time"
)

func main() {
	// Create the audio streamer and the udp listener.
	s, err := CreateStreamer()
	if err != nil {
		fmt.Printf("Failed to initialize audio %v", err)
		os.Exit(2)
	}
	l, err := CreateListener(":12345")
	if err != nil {
		fmt.Printf("Failed to listen %v", err)
		os.Exit(1)
	}

	// Connect the listener to the audio stream.
	go l.Listen(s.Record)
	// Play the audio.
	go s.StartPlaying()
	// Record as well.
	go s.StartRecording(fmt.Sprintf("/tmp/recording-%v.wav", time.Now().Format(time.RFC3339)))

	// Close things cleanly on Ctrl-C. portaudio terminate needs to be called on
	// shutdown.
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c
	fmt.Println("Stopping program")
	s.Stop()
	l.Stop()
}
