package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/gordonklaus/portaudio"
)

// This is a high quality audio quality.
const rate = 44100

// Choose "harmonic notes"
var multipliers = []float64{1.0, 5.0 / 4, 4.0 / 3, 3.0 / 2, 5.0 / 3, 2.0, 5.0 / 2, 3.0}

// Streamer stores the last 256 data points.
// This code intentionally avoids as much heap allocation as possible by
// statically defining all sizes. GCs can cause blips in the audio.
type Streamer struct {
	// Store the last 256 points.
	data        [256]uint32
	dataCounter byte
	totalPoints int64
	counter     int
	// phase is the "x-axis" of the sine curve.
	phase      [8]float64
	includeIds []byte
	quit       chan struct{}
}

// Chose 8 steps along the way from smallest to biggest.
func (s *Streamer) getPercentiles() [8]uint32 {
	// Copy all the values over since we don't want to sort the underlying
	// array. This is "racy" with writes, but we don't want locking.
	d := s.data

	sort.Slice(d[:], func(i, j int) bool { return d[i] > d[j] })
	var steps [8]uint32
	for i := range steps {
		// Take the sorted value at 1/2^i so ends up with the
		// 1, 2, 4, ... 128 values
		pxValue := d[1<<i]
		// Store in reverse order for easier computation later. We base our rate
		// on the P50 (128th) value.
		// Store the inverse of the rate in microseconds.
		steps[7-i] = pxValue
	}
	return steps
}

// If the base is 100 Hz, the 8th term will have a step of 12.8kHz
// Human hearing is ~20 Hz - 20kHz, so bound base step from 50 to 100 Hz.
// which bounds last step to range 128
// 1ms -> 23Hz
// 100ms -> 69Hz
func convertLatencyToStep(micro uint32) float64 {
	rawStep := 30 * math.Log1p(math.Max(float64(micro), 1))
	normal := math.Min(math.Max(rawStep, 100.0), 400.0)
	// Normalize based on the sound base rate.
	return normal / rate
}

// This is called repeatedly with a "small window" of time. We need to fill
// "rate" steps per second. so if step is 1.0, we will have a 1Hz sine wave.
func (s *Streamer) genAudio(out []float32) {
	percentiles := s.getPercentiles()

	// Reset all the values since the same array is reused each time.
	for i := range out {
		out[i] = 0
	}

	baseStep := convertLatencyToStep(percentiles[0])
	prevP := 0.0

	// fill with a superposition of the waves
	// Add all the frequencies together (see fourier transform).
	// Compute the next several steps of the sine waves based on the step and amp.
	for i, p := range percentiles {
		// We want all waves to have the same "period" which is computed by the P50 value.
		// step is a multiple of the base rate, each step is half the previous step.
		// higher P values have higher frequency steps.
		step := baseStep * multipliers[i]

		// amp is the height of the sine curve which is based on ratio from
		// adjacent step. Start with amp 1 for the base, and increase for the
		// others. Don't allow any individual amp to get above 2.0 (your ears
		// will thank me).
		amp := math.Min(float64(p)/prevP, 3.0) - 1
		prevP = float64(p)

		// Periodically print this. Could change to time based instead.
		if s.counter%1000 == 0 {
			fmt.Printf("%d: %0.2f %d\n", i, amp, p)
		}

		for j := range out {
			// get the next output value for this curve and add to the others.
			nextOut := amp * math.Sin(2*math.Pi*s.phase[i])
			out[j] += float32(nextOut)
			// move phase along in small steps update for next time, resetting
			// to 0 so that we can avoid wild swings when we change the step size.
			// Phase always stays between 0 and 1.
			_, s.phase[i] = math.Modf(s.phase[i] + step)
		}
	}
	if s.counter%1000 == 0 {
		fmt.Println("Base freq: ", int(baseStep*rate))
	}
	s.counter++
}

// Record is not thread-safe. The caller of this method should ensure it is
// not called concurrently.
func (s *Streamer) Record(value uint32, id byte) {
	if len(s.includeIds) > 0 {
		found := false
		for _, include := range s.includeIds {
			if id == include {
				found = true
				break
			}
		}
		if !found {
			return
		}

	}
	// This is a data race with the reader, but we don't care  since we are the
	// single writer, and we are writing single values.
	s.data[s.dataCounter] = value
	// This will auto-wrap at 256 which is why we chose byte type.
	s.dataCounter += 1
	s.totalPoints += 1
}

// CreateStreamer creates a new streamer.
func CreateStreamer() (*Streamer, error) {
	err := portaudio.Initialize()
	if err != nil {
		return nil, err
	}
	s := Streamer{quit: make(chan struct{})}
	// Fill some random data, with a normal distribution.
	for i := range s.data {
		s.data[i] = uint32(math.Max(rand.NormFloat64()*10000+10000, 0))
	}
	for i := range &s.phase {
		s.phase[i] = 0.0
	}
	return &s, nil
}

// StartPlaying will play audio with a tone based on the values passed into
// Record.
func (s *Streamer) StartPlaying() {
	stream, err := portaudio.OpenDefaultStream(0, 1, rate, 1024, s.genAudio)
	if err != nil {
		panic(err)
	}
	err = stream.Start()
	if err != nil {
		panic(err)
	}

	// Run until Stop is called.
	<-s.quit
}

// Stop the portaudio device.
func (s *Streamer) Stop() {
	_ = portaudio.Terminate()
	close(s.quit)
}
