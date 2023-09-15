package proxy

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/angrybayblade/tunnel/proxy/headers"
	"github.com/urfave/cli/v2"
)

const DUMMY_KEY string = "0000000000000000000000000000000000000000000"

type ReverseProxy struct {
	addr       Addr
	uri        string
	key        string
	sessionKey string
	quitch     chan struct{}
	waitGroup  *sync.WaitGroup
}

func (rp *ReverseProxy) Connect() {
	conn, err := net.Dial("tcp", rp.uri)
	if err != nil {
		fmt.Println("Failed connecting to the proxy:", err.Error())
		os.Exit(1)
	}

	createRequest := &headers.ProxyHeader{
		Code: headers.RP_REQUEST_CREATE,
		Key:  rp.key,
	}
	_, err = createRequest.Write(conn)
	if err != nil {
		fmt.Println("Failed creating session:", err.Error())
		os.Exit(1)
	}

	createResponse := &headers.ProxyHeader{}
	err = createResponse.Read(conn)
	if err != nil {
		fmt.Println("Could not get the response from the proxy:", err.Error())
		os.Exit(1)
	}

	if createResponse.Code == headers.FP_STATUS_SUCCESS {
		rp.sessionKey = createResponse.Key
		return
	}

	if createResponse.Code == headers.FP_STATUS_AUTH_ERROR {
		fmt.Println("Invalid authentication key provided")
		os.Exit(1)
	}
}

func (rp *ReverseProxy) Listen(id int) {
	var joinRequest *headers.ProxyHeader
	var joinResponse *headers.ProxyHeader
	for {
		proxyDial, err := net.Dial("tcp", rp.uri)
		if err != nil {
			fmt.Println("Failed connecting to the proxy:", err.Error())
			time.Sleep(3 * time.Second)
			continue
		}

		joinRequest = &headers.ProxyHeader{
			Code:    headers.RP_REQUEST_JOIN,
			Key:     rp.sessionKey,
			Message: strconv.Itoa(id),
		}
		_, err = joinRequest.Write(proxyDial)
		if err != nil {
			fmt.Println("Failed joining the proxy pool:", err.Error())
			time.Sleep(3 * time.Second)
			continue
		}

		joinResponse = &headers.ProxyHeader{}
		err = joinResponse.Read(proxyDial)
		if err != nil {
			fmt.Println("Could not get the response from the proxy:", err.Error())
			time.Sleep(3 * time.Second)
			continue
		}

		if joinResponse.Code == headers.FP_STATUS_AUTH_ERROR {
			fmt.Println("Invalid session key provided")
			return
		}

		if joinResponse.Code == headers.FP_STATUS_MAX_CONNECTIONS_LIMIT_REACHED {
			fmt.Println("Max connections limit reached")
			return
		}

		localDial, err := net.Dial("tcp", rp.addr.ToString())
		if err != nil {
			fmt.Println("Error connecting to local server:", err)
			return
		}

		pumpBytes := make([]byte, 1024)
		for {
			n, err := proxyDial.Read(pumpBytes)
			if err != nil {
				break
			}
			localDial.Write(pumpBytes[:n])
			if string(pumpBytes[n-4:n]) == headers.HTTP_HEADER_SEPARATOR {
				break
			}
		}
		for {
			n, err := localDial.Read(pumpBytes)
			if err != nil {
				break
			}
			proxyDial.Write(pumpBytes[:n])
		}
		localDial.Close()
		proxyDial.Close()
	}
}

func (rp *ReverseProxy) CreatePool() {
	rp.waitGroup = new(sync.WaitGroup)
	for id := 0; id < MAX_CONNECTION_POOL_SIZE; id++ {
		go rp.Listen(id)
		rp.waitGroup.Add(1)
	}
}

func (rp *ReverseProxy) Wait() {
	fmt.Println("Started reverse proxy @", "http://"+rp.sessionKey+"."+"localhost")
	rp.waitGroup.Wait()
}

func Forward(cCtx *cli.Context) error {
	var port int = cCtx.Int("port")
	var host string = cCtx.String("host")
	var key string = cCtx.String("key")
	var uri string = cCtx.String("proxy")
	proxy := &ReverseProxy{
		addr: Addr{
			host: host,
			port: port,
		},
		key:    key,
		uri:    uri,
		quitch: make(chan struct{}),
	}
	proxy.Connect()
	proxy.CreatePool()
	proxy.Wait()
	return nil
}