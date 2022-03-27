package webtransport_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lucas-clemente/quic-go/http3"

	"github.com/marten-seemann/webtransport-go"

	"github.com/stretchr/testify/require"
)

// getConn creates a UDP conn for the server to listen on
func getConn(t *testing.T) *net.UDPConn {
	laddr, err := net.ResolveUDPAddr("udp", "localhost:0")
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp", laddr)
	require.NoError(t, err)
	return conn
}

func addHandler(t *testing.T, s *webtransport.Server, connHandler func(*webtransport.Conn)) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/webtransport", func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.Upgrade(w, r)
		if err != nil {
			t.Logf("upgrading failed: %s", err)
			w.WriteHeader(404) // TODO: better error code
			return
		}
		go connHandler(conn)
	})
	s.H3.Handler = mux
}

func newEchoHandler(t *testing.T) func(*webtransport.Conn) {
	return func(conn *webtransport.Conn) {
		for {
			str, err := conn.AcceptStream(context.Background())
			if err != nil {
				break
			}
			_, err = io.CopyBuffer(str, str, make([]byte, 100))
			require.NoError(t, err)
			require.NoError(t, str.Close())
		}
	}
}

// exchangeData opens a new stream on the connection,
// sends data and checks the echoed data.
func sendDataAndCheckEcho(t *testing.T, conn *webtransport.Conn) {
	t.Helper()
	data := make([]byte, 5*1024)
	rand.Read(data)
	str, err := conn.OpenStream()
	require.NoError(t, err)
	str.SetDeadline(time.Now().Add(time.Second))
	_, err = str.Write(data)
	require.NoError(t, err)
	require.NoError(t, str.Close())
	reply, err := io.ReadAll(str)
	require.NoError(t, err)
	require.Equal(t, data, reply)
}

func TestSingleClient(t *testing.T) {
	tlsConf, certPool := getTLSConf(t)
	s := webtransport.Server{
		H3: http3.Server{Server: &http.Server{TLSConfig: tlsConf}},
	}
	defer s.Close()
	addHandler(t, &s, newEchoHandler(t))

	udpConn := getConn(t)
	servErr := make(chan error, 1)
	go func() {
		servErr <- s.Serve(udpConn)
	}()
	// TODO: check err

	d := webtransport.Dialer{
		// TODO: don't use InsecureSkipVerify
		TLSClientConf: &tls.Config{ClientCAs: certPool, InsecureSkipVerify: true},
	}
	url := fmt.Sprintf("https://localhost:%d/webtransport", udpConn.LocalAddr().(*net.UDPAddr).Port)
	rsp, conn, err := d.Dial(context.Background(), url, nil)
	require.NoError(t, err)
	require.Equal(t, 200, rsp.StatusCode)
	sendDataAndCheckEcho(t, conn)
}

func TestMultipleClients(t *testing.T) {
	const numClients = 5
	tlsConf, certPool := getTLSConf(t)
	s := webtransport.Server{
		H3: http3.Server{Server: &http.Server{TLSConfig: tlsConf}},
	}
	defer s.Close()
	addHandler(t, &s, newEchoHandler(t))

	udpConn := getConn(t)
	servErr := make(chan error, 1)
	go func() {
		servErr <- s.Serve(udpConn)
	}()
	// TODO: check err

	var wg sync.WaitGroup
	wg.Add(numClients)
	for i := 0; i < numClients; i++ {
		go func() {
			defer wg.Done()
			d := webtransport.Dialer{
				// TODO: don't use InsecureSkipVerify
				TLSClientConf: &tls.Config{ClientCAs: certPool, InsecureSkipVerify: true},
			}
			url := fmt.Sprintf("https://localhost:%d/webtransport", udpConn.LocalAddr().(*net.UDPAddr).Port)
			rsp, conn, err := d.Dial(context.Background(), url, nil)
			require.NoError(t, err)
			require.Equal(t, 200, rsp.StatusCode)
			sendDataAndCheckEcho(t, conn)
		}()
	}
	wg.Wait()
}