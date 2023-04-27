package main

import (
	"encoding/binary"
	"fmt"
	"github.com/gordonklaus/portaudio"
	"math"
	"math/rand"
	"os"
	"sort"
	"sync"
)

// This is a high quality audio quality.
const rate = 44100

// How frequently we update the sound per second.
const hertz = 100

// Choose "harmonic notes", these could be changed.
var multipliers = []float64{1.0, 5.0 / 4, 4.0 / 3, 3.0 / 2, 5.0 / 3, 2.0, 5.0 / 2, 3.0}

// Streamer stores the last 256 data points.
// This code intentionally avoids as much heap allocation as possible by
// statically defining all sizes. GCs can cause blips in the audio.
type Streamer struct {
	// Store the last 256 points.
	data        [256]uint32
	wrapCounter byte
	counter     int
	// phase is the "x-axis" of the sine curve.
	phase      [8]float64
	includeIds []byte
	wg         sync.WaitGroup
	quit       chan struct{}
	file       *os.File
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
		// adjacent step. Start with amp 1/8 for the base, and increase for the
		// others. Don't allow any individual amp to get above 1/8, or the total
		// to get above 1.
		amp := math.Min(float64(p)/prevP/8, 9.0/8) - 1
		if amp > 0.2 {
			panic(amp)
		}

		prevP = float64(p)

		// Periodically print data. (Remove me)
		if s.counter%441000 == 0 {
			fmt.Printf("%d: %0.2f %d\n", i, amp, p)
		}

		for j := range out {
			// get the next output value for this curve and add to the others.
			diff := float32(amp * math.Sin(2*math.Pi*s.phase[i]))
			if diff > 0.2 {
				fmt.Printf("%d, %d, %d", amp, diff, j)
				panic(diff)
			}

			out[j] += diff
			// move phase along in small steps update for next time, resetting
			// to 0 so that we can avoid wild swings when we change the step size.
			// Phase always stays between 0 and 1.
			_, s.phase[i] = math.Modf(s.phase[i] + step)
		}
	}
	var converted []int16
	converted = make([]int16, len(out))
	if s.file != nil {
		for i, b := range out {
			fmt.Println(b)
			if b > 1.5 {
				b = 1.0
			}

			converted[i] = int16(b * (math.MaxInt16))
		}
	}
	writeToFile(s.file, converted)

	// Every 10 sec spit out the data.
	if s.counter%441000 == 0 {
		fmt.Println("Base freq: ", int(baseStep*rate))
	}
	s.counter += len(out)
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
	s.data[s.wrapCounter] = value
	// This will auto-wrap at 256 which is why we chose byte type.
	s.wrapCounter += 1
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
	s.wg.Add(1)
	stream, err := portaudio.OpenDefaultStream(0, 1, rate, rate/100, s.genAudio)
	if err != nil {
		panic(err)
	}
	err = stream.Start()
	if err != nil {
		panic(err)
	}

	// Run until Stop is called.
	select {
	case <-s.quit:
		_ = portaudio.Terminate()
	}
	s.wg.Done()
}

func writeToFile(file *os.File, data any) {
	err := binary.Write(file, binary.LittleEndian, data)
	if err != nil {
		panic(err)
	}
}

// StartRecording will begin recording the output to a new file.
func (s *Streamer) StartRecording(filename string) {
	s.wg.Add(1)
	fmt.Printf("Writing to file %s", filename)
	// Create a new file with the date as the timestamp.
	file, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	s.file = file

	writeToFile(file, []byte("RIFF")) // WAV header
	writeToFile(file, uint32(0))      // data + header size (filled later)
	writeToFile(file, []byte("WAVE")) // file type
	writeToFile(file, []byte("fmt ")) // format header
	writeToFile(file, uint32(16))     // format data length
	writeToFile(file, uint16(1))      // 16-bit signed PCM
	writeToFile(file, uint16(1))      // mono
	writeToFile(file, uint32(rate))   // sample rate
	writeToFile(file, uint32(rate*2)) // bytes/sample
	writeToFile(file, uint16(2))      // block align
	writeToFile(file, uint16(16))     // bits per sample
	writeToFile(file, []byte("data")) // start of data section
	writeToFile(file, uint32(0))      // length of data (filled later)

	<-s.quit
	dataLen := s.counter * 2
	fmt.Printf("Data len %d", dataLen)

	// Update the header with the amount we wrote.
	_, err = file.Seek(4, 0)
	if err != nil {
		panic(err)
	}
	writeToFile(file, uint32(36+dataLen)) // data + header size
	_, err = file.Seek(40, 0)
	if err != nil {
		panic(err)
	}
	writeToFile(file, uint32(dataLen)) // length of data

	err = file.Close()
	if err != nil {
		panic(err)
	}
	s.wg.Done()
}

// Stop notifies the running loops to stop.
func (s *Streamer) Stop() {
	close(s.quit)
	s.wg.Wait()
}
