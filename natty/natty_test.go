package natty

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/getlantern/waddell"
)

const (
	MESSAGE_TEXT = "Hello World"

	WADDELL_ADDR = "localhost:19543"
)

// TestDirect starts up two local Traversals that communicate with each other
// directly.  Once connected, one peer sends a UDP packet to the other to make
// sure that the connection works.
func TestDirect(t *testing.T) {
	doTest(t, func(offer *Traversal, answer *Traversal) {
		go func() {
			for {
				msg, done := offer.NextMsgOut()
				if done {
					return
				}
				log.Printf("offer -> answer: %s", msg)
				answer.MsgIn(msg)
			}
		}()

		go func() {
			for {
				msg, done := answer.NextMsgOut()
				if done {
					return
				}
				log.Printf("answer -> offer: %s", msg)
				offer.MsgIn(msg)
			}
		}()
	})
}

// TestWaddell starts up two local Traversals that communicate with each other
// using a local waddell server.  Once connected, one peer sends a UDP packet to
// the other to make sure that the connection works.
func TestWaddell(t *testing.T) {
	doTest(t, func(offer *Traversal, answer *Traversal) {
		// Start a waddell server
		server := &waddell.Server{}
		log.Printf("Starting waddell at %s", WADDELL_ADDR)
		listener, err := net.Listen("tcp", WADDELL_ADDR)
		if err != nil {
			t.Fatalf("Unable to listen at %s: %s", WADDELL_ADDR, err)
		}
		go func() {
			err = server.Serve(listener)
			if err != nil {
				t.Fatalf("Unable to start waddell at %s: %s", WADDELL_ADDR, err)
			}
		}()

		offerClient := makeWaddellClient(t)
		answerClient := makeWaddellClient(t)

		// Send from offer -> answer
		go func() {
			for {
				msg, done := offer.NextMsgOut()
				if done {
					return
				}
				log.Printf("offer -> answer: %s", msg)
				offerClient.Send(answerClient.ID(), []byte(msg))
			}
		}()

		// Receive to offer
		go func() {
			for {
				b := make([]byte, 4096+waddell.WADDELL_OVERHEAD)
				msg, err := offerClient.Receive(b)
				if err != nil {
					t.Fatalf("offer unable to receive message from waddell: %s", err)
				}
				offer.MsgIn(string(msg.Body))
			}
		}()

		// Send from answer -> offer
		go func() {
			for {
				msg, done := answer.NextMsgOut()
				if done {
					return
				}
				log.Printf("answer -> offer: %s", msg)
				answerClient.Send(offerClient.ID(), []byte(msg))
			}
		}()

		// Receive to answer
		go func() {
			for {
				b := make([]byte, 4096+waddell.WADDELL_OVERHEAD)
				msg, err := answerClient.Receive(b)
				if err != nil {
					t.Fatalf("answer unable to receive message from waddell: %s", err)
				}
				answer.MsgIn(string(msg.Body))
			}
		}()

	})
}

func doTest(t *testing.T, signal func(*Traversal, *Traversal)) {
	var offer *Traversal
	var answer *Traversal

	var debug io.Writer
	if testing.Verbose() {
		debug = os.Stderr
	}

	offer = Offer(debug)
	defer offer.Close()

	answer = Answer(debug)
	defer answer.Close()

	var answerReady sync.WaitGroup
	answerReady.Add(1)

	var wg sync.WaitGroup
	wg.Add(2)

	// offer processing
	go func() {
		defer wg.Done()
		// Try it with a really short timeout (should error)
		fiveTuple, err := offer.FiveTupleTimeout(5 * time.Millisecond)
		if err == nil {
			t.Errorf("Really short timeout should have given error")
		}

		// Try it again without timeout
		fiveTuple, err = offer.FiveTuple()
		if err != nil {
			t.Errorf("offer had error: %s", err)
			return
		}

		// Call it again to make sure we're getting the same 5-tuple
		fiveTupleAgain, err := offer.FiveTuple()
		if fiveTupleAgain.Local != fiveTuple.Local ||
			fiveTupleAgain.Remote != fiveTuple.Remote ||
			fiveTupleAgain.Proto != fiveTuple.Proto {
			t.Errorf("2nd FiveTuple didn't match original")
		}

		log.Printf("offer got FiveTuple: %s", fiveTuple)
		if fiveTuple.Proto != UDP {
			t.Errorf("Protocol was %s instead of udp", fiveTuple.Proto)
			return
		}
		local, remote, err := udpAddresses(fiveTuple)
		if err != nil {
			t.Error("offer unable to resolve UDP addresses: %s", err)
			return
		}
		answerReady.Wait()
		conn, err := net.DialUDP("udp", local, remote)
		if err != nil {
			t.Errorf("Unable to dial UDP: %s", err)
			return
		}
		for i := 0; i < 10; i++ {
			_, err := conn.Write([]byte(MESSAGE_TEXT))
			if err != nil {
				t.Errorf("offer unable to write to UDP: %s", err)
				return
			}
		}
	}()

	// answer processing
	go func() {
		defer wg.Done()
		fiveTuple, err := answer.FiveTupleTimeout(5 * time.Second)
		if err != nil {
			t.Errorf("answer had error: %s", err)
			return
		}
		if fiveTuple.Proto != UDP {
			t.Errorf("Protocol was %s instead of udp", fiveTuple.Proto)
			return
		}
		log.Printf("answer got FiveTuple: %s", fiveTuple)
		local, _, err := udpAddresses(fiveTuple)
		if err != nil {
			t.Errorf("Error in answer: %s", err)
			return
		}
		conn, err := net.ListenUDP("udp", local)
		if err != nil {
			t.Errorf("answer unable to listen on UDP: %s", err)
			return
		}
		answerReady.Done()
		b := make([]byte, 1024)
		for {
			n, addr, err := conn.ReadFrom(b)
			if err != nil {
				t.Errorf("answer unable to read from UDP: %s", err)
				return
			}
			if addr.String() != fiveTuple.Remote {
				t.Errorf("UDP package had address %s, expected %s", addr, fiveTuple.Remote)
				return
			}
			msg := string(b[:n])
			if msg != MESSAGE_TEXT {
				log.Printf("Got message '%s', expected '%s'", msg, MESSAGE_TEXT)
			}
			return
		}
	}()

	// "Signaling" - this would typically be done using a signaling server like
	// waddell when talking to a remote peer

	signal(offer, answer)

	doneCh := make(chan interface{})
	go func() {
		wg.Wait()
		doneCh <- nil
	}()

	select {
	case <-doneCh:
		return
	case <-time.After(1000 * time.Second):
		t.Errorf("Test timed out")
	}
}

func makeWaddellClient(t *testing.T) *waddell.Client {
	conn, err := net.Dial("tcp", WADDELL_ADDR)
	if err != nil {
		t.Fatalf("Unable to dial waddell: %s", err)
	}
	wc, err := waddell.Connect(conn)
	if err != nil {
		t.Fatalf("Unable to connect to waddell: %s", err)
	}
	return wc
}

func udpAddresses(fiveTuple *FiveTuple) (*net.UDPAddr, *net.UDPAddr, error) {
	if fiveTuple.Proto != UDP {
		return nil, nil, fmt.Errorf("FiveTuple.Proto was not UDP!: %s", fiveTuple.Proto)
	}
	local, err := net.ResolveUDPAddr("udp", fiveTuple.Local)
	if err != nil {
		return nil, nil, fmt.Errorf("Unable to resolve local UDP address %s: %s", fiveTuple.Local)
	}
	remote, err := net.ResolveUDPAddr("udp", fiveTuple.Remote)
	if err != nil {
		return nil, nil, fmt.Errorf("Unable to resolve remote UDP address %s: %s", fiveTuple.Remote)
	}
	return local, remote, nil
}
