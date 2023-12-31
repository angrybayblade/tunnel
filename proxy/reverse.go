package proxy

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/angrybayblade/tunnel/proxy/headers"
)

const DUMMY_KEY string = "0000000000000000000000000000000000000000000"

func toIp(addr string, port string) (string, error) {
	if strings.Contains(addr, ":") {
		split := strings.Split(addr, ":")
		addr = split[0]
		port = split[1]
	}

	ips, err := net.LookupIP(addr)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", errors.New("Error performing IP Lookup")
	}
	ip := ips[len(ips)-1].String() + ":" + port
	return ip, nil
}

type ReverseProxy struct {
	Addr   Addr
	Logger *log.Logger
	Proxy  string
	Key    string
	Quitch chan error

	sessionKey  string
	waitGroup   *sync.WaitGroup
	connections chan int
	proxyIp     string
}

func (rp *ReverseProxy) ProxyURI() string {
	if rp.proxyIp != "" {
		return rp.proxyIp
	}
	rp.proxyIp, _ = toIp(rp.Proxy, "80")
	return rp.proxyIp
}

func (rp *ReverseProxy) Connect() error {
	conn, err := net.Dial("tcp", rp.ProxyURI())
	if err != nil {
		return fmt.Errorf("Failed connecting to the proxy: %w", err)
	}

	createRequest := &headers.ProxyHeader{
		Code: headers.ProxyRequestCreatePool,
		Key:  rp.Key,
	}
	_, err = createRequest.Write(conn)
	if err != nil {
		return fmt.Errorf("Failed creating session: %w", err)
	}

	createResponse := &headers.ProxyHeader{}
	err = createResponse.Read(conn)
	if err != nil {
		return fmt.Errorf("Could not get the response from the proxy: %w", err)
	}

	if createResponse.Code == headers.ProxyResponseAuthError {
		return ErrProxyAuth
	}

	rp.sessionKey = createResponse.Key
	rp.Quitch = make(chan error)
	rp.connections = make(chan int, MaxConnectionPoolSize)
	for id := 0; id < MaxConnectionPoolSize; id++ {
		rp.connections <- id
	}
	return nil
}

func (rp *ReverseProxy) Listen() {
	var id int
	var joinRequest *headers.ProxyHeader
	var joinResponse *headers.ProxyHeader
	var ticker *time.Ticker = time.NewTicker(3 * time.Second)

	fmt.Println("Starting reverse proxy @", "http://"+rp.sessionKey+"."+rp.Proxy)
	for {
		id = <-rp.connections
		for {
			proxyDial, err := net.Dial("tcp", rp.ProxyURI())
			if err != nil {
				rp.Logger.Println("Failed connecting to the proxy:", err.Error())
				<-ticker.C
				continue
			}

			joinRequest = &headers.ProxyHeader{
				Code:    headers.ProxyRequestJoinPool,
				Key:     rp.sessionKey,
				Message: strconv.Itoa(id),
			}
			_, err = joinRequest.Write(proxyDial)
			if err != nil {
				rp.Logger.Println("Failed joining the proxy pool:", err.Error())
				<-ticker.C
				continue
			}

			joinResponse = &headers.ProxyHeader{}
			err = joinResponse.Read(proxyDial)
			if err != nil {
				rp.Logger.Println("Could not get the response from the proxy:", err.Error())
				<-ticker.C
				continue
			}

			if joinResponse.Code == headers.ProxyResponseMaxConnectionsLimitReached {
				rp.Logger.Println("Max connections limit reached")
				break
			}

			if joinResponse.Code == headers.ProxyResponseAuthError {
				rp.Quitch <- ErrProxyInvalidSessionKey
				return
			}

			// Wait until we get request
			pumpBytes := make([]byte, 1)
			proxyDial.Read(pumpBytes)
			go rp.Forward(proxyDial, pumpBytes, id)
			break
		}
	}
}

func (rp *ReverseProxy) Forward(proxyDial net.Conn, pumpBytes []byte, id int) {
	localDial, err := net.Dial("tcp", rp.Addr.ToString())
	if err != nil {
		rp.Logger.Println("Error connecting to local server:", err)
		headers.HttpResponseCannotConnectToLocalserver.Write(proxyDial)
		proxyDial.Close()
		return
	}

	requestHeader := headers.HttpRequestHeader{
		Buffer: pumpBytes,
	}
	requestHeader.Read(proxyDial)
	requestHeader.Write(localDial)
	if requestHeader.Headers["Content-Length"] != "" {
		contenLength, _ := strconv.Atoi(requestHeader.Headers["Content-Length"])
		if contenLength <= HttpRequestPipeChunkSize {
			pumpBytes = make([]byte, contenLength)
			proxyDial.Read(pumpBytes)
			localDial.Write(pumpBytes)
		} else {
			iter := int(math.Floor(float64(contenLength) / float64(HttpRequestPipeChunkSize)))
			pumpBytes = make([]byte, HttpRequestPipeChunkSize)
			for i := 0; i < iter; i++ {
				proxyDial.Read(pumpBytes)
				localDial.Write(pumpBytes)
			}
			pumpBytes = make([]byte, contenLength%HttpRequestPipeChunkSize)
			proxyDial.Read(pumpBytes)
			localDial.Write(pumpBytes)
		}
	}

	pumpBytes = make([]byte, 64)
	for {
		n, err := localDial.Read(pumpBytes)
		if err != nil {
			break
		}
		proxyDial.Write(pumpBytes[:n])
	}
	localDial.Close()
	proxyDial.Close()
	rp.connections <- id
	rp.Logger.Println("/FORWARD Connection:", id, "->", requestHeader.Path, requestHeader.Method, requestHeader.Protocol)
}

func (rp *ReverseProxy) Disconnect() {
	rp.Logger.Println("Disconnecting...")
	conn, err := net.Dial("tcp", rp.ProxyURI())
	if err != nil {
		// The proxy is not running
		return
	}
	deleteSessionRequest := &headers.ProxyHeader{
		Code: headers.ProxyRequestDeletePool,
		Key:  rp.sessionKey,
	}
	deleteSessionRequest.Write(conn)
}
