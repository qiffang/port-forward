package util

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/txn2/txeh"
	"github.com/wushilin/stream"
)

var (
	ADD_VIR_CMD = map[string]func(address VirtualIPAddress) []string{
		"darwin": func(address VirtualIPAddress) []string {
			return []string{address.InterfaceName, "alias", address.IP}
		},
		"linux": func(address VirtualIPAddress) []string {
			return []string{fmt.Sprintf("%s:%d", address.InterfaceName, address.InterfaceIndex), address.IP, "netmask", "255.255.255.0", "up"}
		},
	}

	REMOVE_VIR_CMD = map[string]func(address VirtualIPAddress) []string{
		"darwin": func(address VirtualIPAddress) []string {
			return []string{address.InterfaceName, "-alias", address.IP}
		},
		"linux": func(address VirtualIPAddress) []string {
			return []string{fmt.Sprintf("%s:%d", address.InterfaceName, address.InterfaceIndex), "down"}
		},
	}
)

type VirtualIPAddress struct {
	InterfaceName  string
	InterfaceIndex int
	IP             string
}

func AddVirtualIP(address VirtualIPAddress) error {
	return action(ADD_VIR_CMD, address)
}

func RemoveVirtualIP(address VirtualIPAddress) error {
	return action(REMOVE_VIR_CMD, address)
}

func action(m map[string]func(address VirtualIPAddress) []string, address VirtualIPAddress) error {
	f, ok := m[runtime.GOOS]
	if !ok {
		return errors.New("unsupport platform:" + runtime.GOOS)
	}

	cmd := exec.Command("ifconfig", f(address)...)
	return cmd.Run()
}

func InterfaceName() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return "", err
		}

		ifsName := ""
		stream.FromArray(addrs).Map(func(addr net.Addr) string {
			return addr.String()
		}).Filter(func(addr string) bool {
			return strings.Contains(addr, "127.0.0.1")
		}).Each(func(addr string) {
			ifsName = iface.Name
		})

		return ifsName, nil
	}

	return "", errors.New("empty interface")
}

func GetFreePort() (int, error) {
	ports, err := GetFreePorts(1)
	if err != nil {
		return 0, err
	}

	if len(ports) < 1 {
		return 0, errors.New("get free ports failed")
	}

	return ports[0], nil
}

func GetFreePorts(count int) ([]int, error) {
	var ports []int
	for i := 0; i < count; i++ {
		addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
		if err != nil {
			return nil, err
		}

		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return nil, err
		}
		defer l.Close()
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	return ports, nil
}

type VirtualHost struct {
	*txeh.Hosts
}

func (h *VirtualHost) SyncSave() error {
	hfData := []byte(h.RenderHostsFile())

	h.Lock()
	defer h.Unlock()

	//err := ioutil.WriteFile(h.WriteFilePath, hfData, 0644)
	//if err != nil {
	//	return err
	//}

	f, err := os.OpenFile(h.WriteFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	n, err := f.Write(hfData)
	if err == nil && n < len(hfData) {
		err = io.ErrShortWrite
	}

	if err1 := f.Sync(); err == nil {
		err = err1
	}

	if err1 := f.Close(); err == nil {
		err = err1
	}
	return err
}
