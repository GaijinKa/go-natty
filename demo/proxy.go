package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/getlantern/go-natty/natty"
	"github.com/getlantern/waddell"
)

const (
	// TODO: figure out maximum required size for messages
	MAX_MESSAGE_SIZE = 4096

	READY = "READY"

	TIMEOUT = 15 * time.Second
)

var (
	endianness = binary.LittleEndian

	help        = flag.Bool("help", false, "Get usage help")
	mode        = flag.String("mode", "client", "client or server. Client initiates the NAT traversal. Defaults to client.")
	waddellAddr = flag.String("waddell", "128.199.130.61:443", "Address of waddell signaling server, defaults to 128.199.130.61:443")
	debug       = flag.Bool("debug", false, "Enable debug logging to stderr")

	wc       *waddell.Client
	debugOut io.Writer
)

// message represents a message exchanged during a NAT traversal session
type message []byte

func (msg message) setSessionID(id uint32) {
	endianness.PutUint32(msg[:4], id)
}

func (msg message) getSessionID() uint32 {
	return endianness.Uint32(msg[:4])
}

func (msg message) getData() []byte {
	return msg[4:]
}

func idToBytes(id uint32) []byte {
	b := make([]byte, 4)
	endianness.PutUint32(b[:4], id)
	return b
}

func main() {
	flag.Parse()
	if *help {
		flag.Usage()
		return
	}

	if *debug {
		debugOut = os.Stderr
	}

	connectToWaddell()

	if "server" == *mode {
		runServer()
	} else {
		runClient()
	}
}

func connectToWaddell() {
	conn, err := net.Dial("tcp", *waddellAddr)
	if err != nil {
		log.Fatalf("Unable to dial waddell: %s", err)
	}
	wc, err = waddell.Connect(conn)
	if err != nil {
		log.Fatalf("Unable to connect to waddell: %s", err)
	}
}

func udpAddresses(ft *natty.FiveTuple) (*net.UDPAddr, *net.UDPAddr, error) {
	if ft.Proto != natty.UDP {
		return nil, nil, fmt.Errorf("FiveTuple.Proto was not UDP!: %s", ft.Proto)
	}
	local, err := net.ResolveUDPAddr("udp", ft.Local)
	if err != nil {
		return nil, nil, fmt.Errorf("Unable to resolve local UDP address %s: %s", ft.Local)
	}
	remote, err := net.ResolveUDPAddr("udp", ft.Remote)
	if err != nil {
		return nil, nil, fmt.Errorf("Unable to resolve remote UDP address %s: %s", ft.Remote)
	}
	return local, remote, nil
}
