# find-pingable-IP

The script will try to fetch announced prefix from RIPEstat and perform ping test to a range (v4 and its /24 only).
```
✗ sudo go run main.go -asn=AS13335,AS54113
ASAS13335 Pingable IP: 104.18.0.1
Location: San Francisco, California, US
ASAS54113 Pingable IP: 157.52.68.1
Location: Newark, New Jersey, US
map[US:[{104.18.0.1 AS13335} {157.52.68.1 AS54113}]]
```
