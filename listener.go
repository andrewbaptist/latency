package main

import (
	"encoding/binary"
	"net"
)

type UDPListener struct {
	conn *net.UDPConn
	quit chan struct{}
}

// Listen for data and calls the output func when it receives data until Stop is
// called.
func (l *UDPListener) Listen(output func(uint32, byte)) {
	// Read incoming messages in a loop, allocate the buf only once.
	buf := make([]byte, 5)

	for {
		select {
		case <-l.quit:
			_ = l.conn.Close()
			break
		default:
			n, _, err := l.conn.ReadFromUDP(buf)
			if n != 5 {
				println("Should be 5 bytes, ignoring not: ", n)
				continue
			}
			if err != nil {
				panic(err)
			}
			value := binary.LittleEndian.Uint32(buf[0:4])
			output(value, buf[4])
		}
	}
}

// CreateListener will create a new UDPListener that will listen on the provided
// address.
func CreateListener(addr string) (*UDPListener, error) {
	// Listen for UDP messages on port 12345
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}

	return &UDPListener{
		conn: conn,
		quit: make(chan struct{}),
	}, nil
}

// Stop the listener and close the connection.
func (l *UDPListener) Stop() {
	close(l.quit)
}
