package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/url"
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

type PeeringDBResponse struct {
	Data []struct {
		Website string `json:"website"`
	} `json:"data"`
}
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
	ASN     string `json:"org"`
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

		pingNS := findPingableNS(asNumber)
		var ipFound bool
		if pingNS != "" {
			lookupIP, err := net.LookupIP(pingNS)
			if err != nil {
				fmt.Println("Error:", err)
				return
			}
			var targetIP string
			for _, ip := range lookupIP {
				targetIP = ip.String()
				break
			}
			location := getIPLocation(targetIP)
			if strings.Contains(location.ASN, asNumber) {
				ipFound = true
				countryMap[location.Country] = append(countryMap[location.Country], CountryIPASN{IP: pingNS, ASN: asNumber})

			}
		}
		if ipFound {
			continue
		}
		// Step 2: Ping the IP addresses within the prefixes
		pingTarget := findPingableIP(prefixes, asNumber)

		// Step 3: Retrieve location information of the first pingable IP
		if pingTarget != "" {
			var location IPInfo
			location = getIPLocation(pingTarget)
			countryMap[location.Country] = append(countryMap[location.Country], CountryIPASN{IP: pingTarget, ASN: asNumber})
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
func findPingableNS(asn string) string {
	asnURL := getDomainFromPeeringDB(strings.Split(asn, "AS")[1])
	parsedURL, err := url.Parse(asnURL)
	if err != nil {
		fmt.Println("Error parsing URL:", err)
		return ""
	}

	// Get the host (which includes the domain and port if present)
	domain := parsedURL.Hostname() // This will give you "sub.example.com"

	if domain != "" {
		if strings.Contains(domain, "www") {
			domain = strings.Split(domain, "www.")[1]

		}
	}
	nsServer := getNSServer(domain)
	results := parallelPing([]string{nsServer})
	fmt.Println(results)
	for _, reachable := range results {
		if reachable {
			return nsServer
		} else {
			fmt.Printf("%s is not reachable\n", nsServer)
		}
	}
	return ""
}

// findPingableIP finds the first pingable IP within the list of prefixes
func findPingableIP(prefixes []string, asn string) string {
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

func getNSServer(domain string) string {
	nsRecords, err := net.LookupNS(domain)
	if err != nil {
		fmt.Printf("Failed to perform NS lookup: %v\n", err)
		return ""
	}
	for _, ns := range nsRecords {
		fmt.Printf("  %s\n", ns.Host)
		return ns.Host
	}
	return ""
}

func getDomainFromPeeringDB(asn string) string {
	url := fmt.Sprintf("https://www.peeringdb.com/api/net?asn=%s", asn)
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("Failed to query PeeringDB API: %v", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Failed to read response body: %v", err)
	}

	// Parse the JSON response
	var pdbResponse PeeringDBResponse
	if err := json.Unmarshal(body, &pdbResponse); err != nil {
		log.Fatalf("Failed to parse JSON response: %v", err)
	}

	// Extract and print the website
	if len(pdbResponse.Data) > 0 && pdbResponse.Data[0].Website != "" {
		fmt.Printf("Website for ASN %s: %s\n", asn, pdbResponse.Data[0].Website)
		return pdbResponse.Data[0].Website
	} else {
		fmt.Printf("No website found for ASN %s\n", asn)
	}
	return ""
}
