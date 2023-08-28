// Package dnsupdate is a utilty daemon to update DNS records on DigitalOcean.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/carlmjohnson/versioninfo"
	"github.com/digitalocean/godo"
	log "github.com/gleich/logoru"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

var (
	configFile     = flag.String("config", "/dnsupdate.toml", "path to configuration file.")
	updateInterval = flag.Duration("interval", 30*time.Minute, "update interval.")

	config *Config
)

// List of public IP checker services.
var services = []string{
	"ifconfig.co",
	"ipinfo.io/ip",
	"ifconfig.me",
}

// DomainRecord is the type for DigitalOcean API.
type DomainRecord struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	Data string `json:"data"`
}

// HostConfig is configuration for a single host to update.
type HostConfig struct {
	Interface string
	Domain    string
}

// Config is configuration for the update daemon.
type Config struct {
	AuthToken string
	Hosts     map[string]HostConfig
}

func loadConfig(configFilePath string) (*Config, error) {
	viper.SetConfigFile(configFilePath)
	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func validateConfig(conf *Config) error {
	if conf.AuthToken == "" {
		return fmt.Errorf("provider auth token not set")
	}
	if len(conf.Hosts) < 1 {
		return fmt.Errorf("no hosts configuration set")
	}
	for name, host := range conf.Hosts {
		if name == "" {
			return fmt.Errorf("invalid hostname '%s'", name)
		}
		if host.Domain == "" {
			return fmt.Errorf("domain not set for hostname '%s'", name)
		}
	}
	return nil
}

func periodicFunction(interval time.Duration, stopChan chan struct{}) {
	runUpdate()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			runUpdate()
		case <-stopChan:
			return
		}
	}
}

func runUpdate() {
	log.Info("running update")
	if config == nil {
		log.Error("no configuration available")
		return
	}
	for name, host := range config.Hosts {
		log.Info("updating address for", name)
		var (
			ipAddr string
			err    error
		)
		if host.Interface != "" {
			log.Info("checking private ip for interface", host.Interface)
			ipAddr, err = resolvePrivateIP(host.Interface)
		} else {
			log.Info("checking public ip for current host", host.Interface)
			ipAddr, err = resolvePublicIP()
		}
		if err != nil {
			log.Error("error resolving ip address", err)
			continue
		}
		log.Info("resolved ip address", ipAddr)
		if err = updateDNSRecord(host.Domain, name, ipAddr); err != nil {
			log.Error(err)
		}
	}
	log.Info("finished all updates")
}

func resolvePublicIPwithService(service string) (string, error) {
	resp, err := http.Get("http://" + service)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	ip := strings.TrimSpace(string(body))
	return ip, nil
}

func resolvePublicIP() (string, error) {
	var (
		ipAddr string
		err    error
	)
	for _, service := range services {
		log.Info("resolving public ip with", service)
		ipAddr, err = resolvePublicIPwithService(service)
		if err == nil && ipAddr != "" {
			return ipAddr, nil
		}
		log.Error("failed to resolve public ip with", service, err)
	}
	return "", fmt.Errorf("couldn't resolve public ip address")
}

func resolvePrivateIP(interfaceName string) (string, error) {
	interfaceObj, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return "", err
	}

	addrs, err := interfaceObj.Addrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return ipNet.IP.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no suitable IP address found for interface %s", interfaceName)
}

func updateDNSRecord(domain string, name string, addr string) error {
	fqdn := name + "." + domain
	token := config.AuthToken
	log.Info("using token", token)
	client := godo.NewFromToken(token)
	ctx := context.Background()

	opts := &godo.ListOptions{
		Page:    1,
		PerPage: 1,
	}
	records, _, err := client.Domains.RecordsByTypeAndName(ctx, domain, "A", fqdn, opts)
	if err != nil {
		return errors.Wrapf(err, "error looking up DNS record for %s", fqdn)
	}
	req := &godo.DomainRecordEditRequest{
		Type: "A",
		Name: name,
		Data: addr,
	}
	if len(records) < 1 {
		log.Info("host", fqdn, "not found, will create new record")
		record, _, err := client.Domains.CreateRecord(ctx, domain, req)
		if err != nil {
			return errors.Wrapf(err, "error creating record for %s", fqdn)
		}
		log.Info("updated record", record.ID, "for", fqdn, "to", addr)
	} else {
		record := records[0]
		log.Info("existing record", record.ID, "found for", fqdn)
		if record.Data == addr {
			log.Info("new address matches existing address", addr, "skipping update.")
			return nil
		}
		_, _, err := client.Domains.EditRecord(ctx, domain, record.ID, req)
		if err != nil {
			return errors.Wrapf(err, "error updating record for %s", fqdn)
		}
		log.Info("updated record", record.ID, "for", fqdn, "to", addr)
	}
	return nil
}
func main() {
	flag.Parse()

	log.Info(fmt.Sprintf("dnsupdate v(%s) (built: %s)", versioninfo.Short(), versioninfo.LastCommit))

	conf, err := loadConfig(*configFile)
	if err != nil {
		log.Error("failed to load config file", err)
		os.Exit(1)
	}
	if err := validateConfig(conf); err != nil {
		log.Error("invalid config", err)
		os.Exit(1)
	}
	config = conf
	log.Debug("using config:\n", config)

	stopChan := make(chan struct{})
	go periodicFunction(*updateInterval, stopChan)

	log.Info("dnsupdate running every", *updateInterval)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	close(stopChan)
	log.Info("dnsupdate exiting...")
}
