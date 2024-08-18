package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-ping/ping"
)

const (
	ripeAPIURLTemplate   = "https://stat.ripe.net/data/announced-prefixes/data.json?data_overload_limit=ignore&resource=%s&starttime=%d&min_peers_seeing=10"
	ipInfoAPIURLTemplate = "https://ipinfo.io/%s/json"
)

type PrefixData struct {
	Data struct {
		Prefixes []struct {
			Prefix string `json:"prefix"`
		} `json:"prefixes"`
	} `json:"data"`
}

type IPInfo struct {
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
}

type CountryIPASN struct {
	IP  string
	ASN string
}

func main() {
	asn := flag.String("asn", "", "ASN to retrieve prefixes and ping IPs")
	flag.Parse()

	if *asn == "" {
		log.Fatal("ASN is required. Use -asn flag to provide an ASN.")
	}
	startTime := time.Now().Add(-24 * time.Hour).Unix()
	countryMap := make(map[string][]CountryIPASN)

	var asnList []string
	if strings.Contains(*asn, ",") {
		asnList = strings.Split(*asn, ",")
	} else {
		asnList = append(asnList, *asn)
	}
	for i := 0; i < len(asnList); i++ {
		asNumber := asnList[i]
		prefixes := fetchPrefixes(asNumber, startTime)

		// Step 2: Ping the IP addresses within the prefixes
		pingableIP := findPingableIP(prefixes)

		// Step 3: Retrieve location information of the first pingable IP
		if pingableIP != "" {
			location := getIPLocation(pingableIP)
			countryMap[location.Country] = append(countryMap[location.Country], CountryIPASN{IP: pingableIP, ASN: asNumber})
			fmt.Printf("AS%s Pingable IP: %s\nLocation: %s, %s, %s\n", asNumber, pingableIP, location.City, location.Region, location.Country)
		} else {
			fmt.Println("No pingable IP found.")
		}
	}
	fmt.Println(countryMap)
}

// fetchPrefixes fetches the announced prefixes for a given ASN from RIPE
func fetchPrefixes(asn string, startTime int64) []string {
	url := fmt.Sprintf(ripeAPIURLTemplate, asn, startTime)
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("Error fetching prefixes: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response: %v", err)
	}

	var data PrefixData
	if err := json.Unmarshal(body, &data); err != nil {
		log.Fatalf("Error parsing JSON: %v", err)
	}

	var prefixes []string
	for _, prefix := range data.Data.Prefixes {
		prefixes = append(prefixes, prefix.Prefix)
	}

	return prefixes
}

// findPingableIP finds the first pingable IP within the list of prefixes
func findPingableIP(prefixes []string) string {
	concurrent := 10
	for _, prefix := range prefixes {
		if strings.Contains(prefix, ":") {
			continue
		}
		ipRange := strings.Split(prefix, "0/")[0]
		for i := 1; i < 255; i++ {
			var pingList []string
			if i != 1 {
				for j := 0; j < concurrent; j++ {
					pingList = append(pingList, ipRange+strconv.Itoa(i))
					i++
				}
			} else {
				pingList = append(pingList, ipRange+strconv.Itoa(i))
			}
			results := parallelPing(pingList)

			for ip, reachable := range results {
				if reachable {
					return ip
				} else {
					fmt.Printf("%s is not reachable\n", ip)
				}
			}
		}
	}
	return ""
}

// parallelPing pings a list of IPs concurrently and returns a map of results
func parallelPing(ips []string) map[string]bool {
	results := make(map[string]bool)
	var wg sync.WaitGroup
	mu := &sync.Mutex{}

	for _, ip := range ips {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			reachable := isReachable(ip)
			mu.Lock()
			results[ip] = reachable
			mu.Unlock()
		}(ip)
	}

	wg.Wait()
	return results
}

// isReachable uses go-ping to check if an IP is reachable
func isReachable(ip string) bool {
	pinger, err := ping.NewPinger(ip)
	if err != nil {
		fmt.Printf("ERROR: %s\n", err.Error())
		return false
	}
	pinger.Count = 1
	pinger.Timeout = time.Second
	pinger.SetPrivileged(true) // Required for privileged ICMP requests

	err = pinger.Run() // Blocks until finished
	if err != nil {
		fmt.Printf("ERROR: %s\n", err.Error())
		return false
	}

	stats := pinger.Statistics() // Get send/receive/rtt stats
	return stats.PacketsRecv > 0
}

// getIPLocation retrieves the geographical location of an IP using ipinfo.io
func getIPLocation(ip string) IPInfo {
	url := fmt.Sprintf(ipInfoAPIURLTemplate, ip)
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("Error fetching IP location: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response: %v", err)
	}

	var ipInfo IPInfo
	if err := json.Unmarshal(body, &ipInfo); err != nil {
		log.Fatalf("Error parsing JSON: %v", err)
	}

	return ipInfo
}
