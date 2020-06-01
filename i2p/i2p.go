package i2p

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"text/template"
)

const configFormat = `
{{ range .EepSites }}
[{{ .ServiceName }}]
type = http
host = {{ .ServiceClusterIP }}
port = {{ .ServicePort }}
keys = {{ .ServiceName }}.dat
{{ end }}
`

var configTemplate = template.Must(template.New("config").Parse(configFormat))

type EepSite struct {
	ServiceName      string
	ServiceNamespace string
	ServiceClusterIP string
	ServiceDir       string
	ServicePort      int
	PublicPort       int
}

type I2pConfiguration struct {
	EepSites map[string]EepSite
}

func NewI2pConfiguration() I2pConfiguration {
	return I2pConfiguration{
		EepSites: make(map[string]EepSite),
	}
}

func (i *I2pConfiguration) AddService(name, serviceName, namespace, clusterIP string, servicePort, publicPort int) *EepSite {
	s := EepSite{
		ServiceName:      serviceName,
		ServiceNamespace: namespace,
		ServiceClusterIP: clusterIP,
		ServiceDir:       fmt.Sprint("/var/lib/i2pd"),
		ServicePort:      servicePort,
		PublicPort:       publicPort,
	}
	i.EepSites[fmt.Sprintf("%s/%s", namespace, name)] = s
	return &s
}

func (i *I2pConfiguration) RemoveService(name string) {
	err := os.RemoveAll("/var/lib/i2pd/tunnels.conf")
	if err != nil {
		fmt.Printf("error removing dir: %v", err)
	}
	delete(i.EepSites, name)
}

func (i *I2pConfiguration) GetConfiguration() string {
	var tmp bytes.Buffer
	configTemplate.Execute(&tmp, i)
	return tmp.String()
}

// TODO:
// 1. Add function to edit tunnels.conf file
func (i *I2pConfiguration) SaveConfiguration() {
	file, err := os.Create("/var/lib/i2pd/tunnels.conf")
	if err != nil {
		log.Fatal("Cannot create file", err)
	}
	defer file.Close()

	configTemplate.Execute(file, i)
}

func (s *EepSite) FindHostname() (string, error) {
	var ErrServiceNotFound = errors.New("Service not found")

	resp, err := http.Get("http://127.0.0.1:7070/?page=i2p_tunnels")
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	b := strings.Fields((string(body)))

	for i, n := range b {
		if strings.Contains(n, s.ServiceName) {
			return fmt.Sprintf("%s.b32.i2p", b[i][strings.Index(b[i], "b32=")+4:strings.Index(b[i], s.ServiceName)-2]), nil
		} else {
			return "", ErrServiceNotFound
		}

	}

	return "", ErrServiceNotFound
}
