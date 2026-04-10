package security

import (
	"log"
	"net"
	"os"

	"github.com/oschwald/geoip2-golang"
)

var (
	GeoDB *geoip2.Reader
)

func InitGeoIP(dbPath string) error {
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Printf("GeoIP DB not found at %s. Geo blocking disabled.", dbPath)
		return nil
	}

	db, err := geoip2.Open(dbPath)
	if err != nil {
		return err
	}
	GeoDB = db
	return nil
}

func CloseGeoIP() {
	if GeoDB != nil {
		GeoDB.Close()
	}
}

func LookupGeo(ip net.IP) (*geoip2.City, error) {
	if GeoDB == nil {
		return nil, nil
	}
	return GeoDB.City(ip)
}

type GeoContext struct {
	Country   string
	City      string
	Latitude  float64
	Longitude float64
	ASN       uint
	ISP       string
}

func GetGeoContext(conn net.Conn) *GeoContext {
	if GeoDB == nil || conn == nil {
		return nil
	}
	addr := conn.RemoteAddr()
	if addr == nil {
		return nil
	}
	host, _, _ := net.SplitHostPort(addr.String())
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	rec, err := LookupGeo(ip)
	if err != nil || rec == nil {
		return nil
	}

	ctx := &GeoContext{
		Country:   rec.Country.IsoCode,
		City:      rec.City.Names["en"],
		Latitude:  rec.Location.Latitude,
		Longitude: rec.Location.Longitude,
	}

	// Try to get ASN if available (some GeoIP City DBs don't have it)
	// If the DB supports it, we could try:
	// recASN, _ := GeoDB.ASN(ip) ...
	// But GeoDB is a *geoip2.Reader which might only be opened for City.

	return ctx
}
