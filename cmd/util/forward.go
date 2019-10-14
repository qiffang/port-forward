package util

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/txn2/txeh"

	v1 "k8s.io/api/core/v1"

	"github.com/pkg/errors"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport/spdy"

	"github.com/qiffang/tidb-operator/pkg/label"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/wushilin/stream"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/portforward"
)

const (
	TIKV_PORT = 20160
	PD_PORT   = 2379
	START_IP  = "172.16.123.%d"
)

var (
	interfaceName string
	vhosts        *VirtualHost
)

type Result struct {
	r map[string]VirtualIPAddress
	sync.Mutex
}

func (r *Result) Add(key string, value VirtualIPAddress) {
	r.Lock()
	defer r.Unlock()

	r.r[key] = value
}

func (r *Result) GetResult() map[string]VirtualIPAddress {
	return r.r
}

func Start(path, namespace string) {
	fmt.Println("Start...")
	runtime.GOMAXPROCS(runtime.NumCPU())
	pf, err := New(path, namespace)
	if err != nil {
		log.Fatal(err)
	}
	defer pf.Stop()

	result, err := pf.TiDBClusterPortForward()
	if err != nil {
		log.Fatal(err)
	}

	ifsName, err := InterfaceName()
	if err != nil {
		log.Fatal(err)
	}
	interfaceName = ifsName

	h, err := txeh.NewHostsDefault()
	if err != nil {
		log.Fatal(err)
	}
	vhosts = &VirtualHost{
		h,
	}

	stop := make(chan os.Signal)
	signal.Notify(stop, os.Interrupt, os.Kill, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	index := 1

	results := &Result{
		r: make(map[string]VirtualIPAddress),
	}

	defer func() {
		CleanUp(nil, results)
	}()
	list, err := Attach(result.pd, index, results, PD_PORT, stop)
	if err != nil {
		panic(err)
	}

	c := color.New(color.FgRed)
	c.Println("pdList: " + strings.Join(list, ","))

	_, err = Attach(result.tikv, index+len(list), results, TIKV_PORT, stop)
	if err != nil {
		panic(err)
	}

	for s := range stop {
		CleanUp(s, results)
	}
}

func CleanUp(s os.Signal, results *Result) {
	fmt.Println("exit", s)
	for key, value := range results.GetResult() {
		vhosts.RemoveHost(key)
		RemoveVirtualIP(value)
	}

	vhosts.Save()
	fmt.Println("done.")
	os.Exit(0)
}

func Attach(result map[string]int, seq int, results *Result, port int, stop chan os.Signal) ([]string, error) {
	list := make([]string, 0)
	waitGroup := &sync.WaitGroup{}
	waitGroup.Add(len(result))

	for add, forward := range result {
		ip := fmt.Sprintf(START_IP, seq)
		address := VirtualIPAddress{
			InterfaceName:  interfaceName,
			InterfaceIndex: seq,
			IP:             ip,
		}

		err := AddVirtualIP(address)
		if err != nil {
			fmt.Println("add virtual ip failed", err)
			stop <- os.Interrupt
		}

		vhosts.AddHost(ip, add)
		vhosts.SyncSave()

		results.Add(add, address)

		ap := fmt.Sprintf("%s:%d", add, port)
		go RedirectPort("tcp", ap, forward, waitGroup, stop)

		list = append(list, ap)
		seq++
	}

	waitGroup.Wait()

	return list, nil
}

type PortForwardResult struct {
	tikv map[string]int
	pd   map[string]int
}

type PortForward struct {
	namespace string
	config    *rest.Config
	stop      chan struct{}
	kubeCli   *kubernetes.Clientset
}

func New(kubeconfigPath string, namespace string) (*PortForward, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	kubeCli, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	return &PortForward{
		namespace: namespace,
		kubeCli:   kubeCli,
		config:    cfg,
		stop:      make(chan struct{}, 1),
	}, nil
}

func (p *PortForward) TiDBClusterPortForward() (*PortForwardResult, error) {
	tikvOptions := metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=tikv", label.ComponentLabelKey)}

	tikvServices, err := p.kubeCli.CoreV1().Services(p.namespace).List(tikvOptions)
	if err != nil {
		return nil, err
	}

	tikvService := ""
	stream.FromArray(tikvServices.Items).Map(func(svc v1.Service) string {
		return svc.Name
	}).Filter(func(svc string) bool {
		return strings.Contains(svc, "peer")
	}).Each(func(svc string) {
		tikvService = svc
	})

	if tikvService == "" {
		return nil, errors.New("do not found tikv service")
	}

	tikvPods, err := p.kubeCli.CoreV1().Pods(p.namespace).List(tikvOptions)
	if err != nil {
		return nil, err
	}

	pdOptions := metav1.ListOptions{LabelSelector: fmt.Sprintf("%s=pd", label.ComponentLabelKey)}

	pdServices, err := p.kubeCli.CoreV1().Services(p.namespace).List(pdOptions)
	if err != nil {
		return nil, err
	}

	pdService := ""
	stream.FromArray(pdServices.Items).Map(func(svc v1.Service) string {
		return svc.Name
	}).Filter(func(svc string) bool {
		return strings.Contains(svc, "peer")
	}).Each(func(svc string) {
		pdService = svc
	})

	pdPods, err := p.kubeCli.CoreV1().Pods(p.namespace).List(pdOptions)
	if err != nil {
		return nil, err
	}

	r := &PortForwardResult{
		tikv: make(map[string]int, len(tikvPods.Items)),
		pd:   make(map[string]int, len(pdPods.Items)),
	}

	for _, pod := range tikvPods.Items {
		if pod.Status.Phase != v1.PodRunning {
			continue
		}

		destinationPort, err := GetFreePort()
		if err != nil {
			return nil, err
		}

		if err := p.portforward(pod.Name, TIKV_PORT, destinationPort); err != nil {
			return nil, err
		}

		r.tikv[fmt.Sprintf("%s.%s.%s.svc", pod.Name, tikvService, p.namespace)] = destinationPort
	}

	for _, pod := range pdPods.Items {
		if pod.Status.Phase != v1.PodRunning {
			continue
		}

		destinationPort, err := GetFreePort()
		if err != nil {
			return nil, err
		}

		if err := p.portforward(pod.Name, PD_PORT, destinationPort); err != nil {
			return nil, err
		}

		r.pd[fmt.Sprintf("%s.%s.%s.svc", pod.Name, pdService, p.namespace)] = destinationPort
	}

	return r, nil
}

func (p *PortForward) portforward(pod string, remotePort, localPort int) error {
	url := p.kubeCli.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(p.namespace).
		Name(pod).
		SubResource("portforward").
		URL()

	transport, upgrader, err := spdy.RoundTripperFor(p.config)
	if err != nil {
		return err
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)

	ports := []string{
		fmt.Sprintf("%d:%d", localPort, remotePort),
	}

	readyChan := make(chan struct{}, 1)
	errChan := make(chan error, 1)

	pf, err := portforward.NewOnAddresses(dialer, []string{"localhost"}, ports, p.stop, readyChan, os.Stdout, os.Stderr)
	if err != nil {
		return errors.Wrap(err, "Could not port forward into pod")
	}

	go func() {
		errChan <- pf.ForwardPorts()
	}()

	select {
	case err = <-errChan:
		return errors.Wrap(err, "Could not create port forward")
	case <-readyChan:
		return nil
	}

	return nil
}

func (p PortForward) Stop() {
	p.stop <- struct{}{}
}

func RedirectPort(proto, address string, portforward int, waitGroup *sync.WaitGroup, stop chan os.Signal) {
	time.Sleep(time.Second * 15)
	ln, err := net.Listen(proto, address)
	waitGroup.Done()

	if err != nil {
		log.Println("listen tcp failed", err)
		stop <- os.Interrupt
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("accept tcp failed", err)
			stop <- os.Interrupt
		}

		go handleRequest(conn, portforward)
	}
}

func handleRequest(conn net.Conn, port int) {
	proxy, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))

	if err != nil {
		panic(err)
	}

	fmt.Println("proxy connected")
	go copyIO(conn, proxy)
	go copyIO(proxy, conn)
}

func copyIO(src, dest net.Conn) {
	defer src.Close()
	defer dest.Close()
	io.Copy(src, dest)
}
