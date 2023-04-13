package main

import (
	"encoding/binary"
	"fmt"
	"github.com/gordonklaus/portaudio"
	"math"
	"math/rand"
	"net"
	"sort"
)

// This code intentionally avoids as much heap allocation as possible by
// statically defining all sizes. GCs can cause blips in the audio.

// Store the last 256 points
// TODO: Initialize all to 0.
var data [256]float64
var dataCounter byte
var totalPoints int64
var counter = 0

// phase is the "x-axis" of the sine curve.
var phase [8]float64

// Chose 8 steps along the way from smallest to biggest.
func getPercentiles() [8]float64 {
	// Copy all the values over since we don't want to sort the underlying
	// array. This is "racy" with writes, but probably OK.
	d := data

	sort.Slice(d[:], func(i, j int) bool { return d[i] > d[j] })
	var steps [8]float64
	for i := range steps {
		// Take the sorted value at 1/2^i so ends up with the
		// 1, 2, 4, ... 128 values
		pxValue := math.Max(d[1<<i], 1)
		// Store in reverse order for easier computation later. We base our rate
		// on the P50 (128th) value.
		// Store the inverse of the rate in microseconds.
		steps[7-i] = pxValue
	}
	return steps
}

// This is a fairly high quality audio quality.
const rate = 44100

// If the base is 100 Hz, the 8th term will have a step of 12.8kHz
// Human hearing is ~20 Hz - 20kHz, so bound base step from 50 to 100 Hz.
// which bounds last step to range 128
// 1ms -> 23Hz
// 100ms -> 69Hz
func convertLatencyToStep(micro float64) float64 {
	rawStep := 30 * math.Log1p(micro)
	normal := math.Min(math.Max(rawStep, 100.0), 400.0)
	// Normalize based on the sound base rate.
	return normal / rate
}

// Choose "harmonic notes"
var multipliers = []float64{1.0, 5.0 / 4, 4.0 / 3, 3.0 / 2, 5.0 / 3, 2.0, 5.0 / 2, 3.0}

// This is called repeatedly with a "small window" of time. We need to fill
// "rate" steps per second. so if step is 1.0, we will have a 1Hz sine wave.
func genAudio(out []float32) {
	percentiles := getPercentiles()

	// Reset all the values since the same array is reused each time.
	for i := range out {
		out[i] = 0
	}

	baseStep := convertLatencyToStep(percentiles[0])
	prevP := 0.0

	var i int
	var p float64
	// fill with a superposition of the waves
	// Add all the frequencies together (see fourier transform).
	// Compute the next several steps of the sine waves based on the step and amp.
	for i, p = range percentiles {
		// We want all waves to have the same "period" which is computed by the P50 value.
		// step is a multiple of the base rate, each step is half the previous step.
		// higher P values have higher frequency steps.
		step := baseStep * multipliers[i]

		// amp is the height of the sine curve which is based on ratio from
		// adjacent step. Start with amp 1 for the base, and increase for the
		// others. Don't allow any individual amp to get above 2.0 (your ears
		// will thank me).
		amp := math.Min(p/prevP, 3.0) - 1
		prevP = p

		if counter%1000 == 0 {
			fmt.Printf("%d: %0.2f (%0.2f)\n", i, amp, p)
		}

		for j := range out {
			// get the next output value for this curve and add to the others.
			nextOut := amp * math.Sin(2*math.Pi*phase[i])
			out[j] += float32(nextOut)
			// move phase along in small steps update for next time, resetting
			// to 0 so that we can avoid wild swings when we change the step size.
			// Phase always stays between 0 and 1.
			_, phase[i] = math.Modf(phase[i] + step)
		}
	}
	if counter%1000 == 0 {
		fmt.Println("Base freq: ", int(baseStep*rate))
	}
	counter++
	//	println(out[0])

}

// This listens for data and updates the data array with it.
func listenDataUDP() {
	// Listen for UDP messages on port 12345
	addr, err := net.ResolveUDPAddr("udp", ":12345")
	if err != nil {
		panic(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	// Read incoming messages in a loop, allocate the buf only once.
	buf := make([]byte, 5)

	for {
		n, _, err := conn.ReadFromUDP(buf)
		if n != 5 {
			println("Should be 5 bytes, ignoring not: ", n)
			continue
		}
		if err != nil {
			panic(err)
		}
		value := binary.LittleEndian.Uint32(buf[0:4])
		// This is a data race with the reader, but we don't care too much...
		// since we are the single writer and we are writing single values.
		data[dataCounter] = float64(value)
		// This will auto-wrap at 256 which is why we chose byte type.
		dataCounter += 1
		totalPoints += 1
	}
}

func main() {
	err := portaudio.Initialize()
	if err != nil {
		panic(err)
	}
	defer portaudio.Terminate()
	// Initialize all our points to 0 to start.
	for i := range data {
		data[i] = rand.NormFloat64()*10000 + 10000
		//data[i] = 0
	}
	go listenDataUDP()

	stream, err := portaudio.OpenDefaultStream(0, 1, rate, 1024, genAudio)
	if err != nil {
		panic(err)
	}
	defer stream.Close()
	err = stream.Start()
	if err != nil {
		panic(err)
	}

	// Run forever
	select {}
}
