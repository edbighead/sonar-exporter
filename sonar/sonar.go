package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	b64 "encoding/base64"

	client "github.com/influxdata/influxdb1-client"
	ini "gopkg.in/ini.v1"
)

// Sonar represents response object from sonarqube rest api.
type Sonar struct {
	Component struct {
		Key      string
		Name     string
		Measures []Measure
		Type     string
	}
}

// Measure represents a part of respose, defined in metricKeys
type Measure struct {
	Metric string
	Value  string
}

//Converts sonar api response strings to float
func StringToFloat(metric, value string) float64 {
	var alert = map[string]float64{
		"OK":    1,
		"WARN":  0.5,
		"ERROR": 0,
	}

	var val float64
	var err error

	if metric != "alert_status" {
		val, err = strconv.ParseFloat(value, 64)
		if err != nil {
		}
	} else {
		val = alert[value]
	}

	return val
}

// GetMeasures sends mapped Sonar object from response to channel
func GetMeasures(url, componentType, token string, ch chan Sonar) {

	token = "Basic " + token
	req, err := http.NewRequest("GET", url, nil)

	req.Header.Add("Authorization", token)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error on response.\n[ERROR] -", err)
	}

	body, _ := ioutil.ReadAll(resp.Body)

	var s Sonar

	json.Unmarshal(body, &s)

	s.Component.Type = componentType

	ch <- s

}

// ClientWrite inserts measures into influx database
func ClientWrite(databaseURL, databse string, projects []Sonar) {
	host, err := url.Parse(fmt.Sprintf("http://%s", databaseURL))
	if err != nil {
		log.Print(err)
	}

	con, err := client.NewClient(client.Config{URL: *host, Timeout: 300 * time.Second})
	if err != nil {
		log.Print(err)
	}

	var (
		pts = []client.Point{}
	)

	for _, p := range projects {
		key := p.Component.Key
		cType := p.Component.Type
		for _, m := range p.Component.Measures {
			metric := m.Metric
			value := StringToFloat(metric, m.Value)
			point := client.Point{
				Measurement: metric,
				Tags: map[string]string{
					"project_key":    key,
					"component_type": cType,
				},
				Fields: map[string]interface{}{
					"val": value,
				},
				Time:      time.Now(),
				Precision: "n",
			}
			pts = append(pts, point)
		}
	}

	bps := client.BatchPoints{
		Points:   pts,
		Database: databse,
	}
	_, err = con.Write(bps)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	cfg, err := ini.Load("config.ini")
	if err != nil {
		fmt.Printf("Fail to read file: %v", err)
		os.Exit(1)
	}
	var divided [][]string

	start := time.Now()
	repos := cfg.SectionStrings()[1:]
	chunkSize := cfg.Section("").Key("chunks").MustInt(9999)
	sonarURL := cfg.Section("").Key("sonarUrl").String()
	database := cfg.Section("").Key("dbName").String()
	databaseURL := cfg.Section("").Key("dbHost").String() + ":" + cfg.Section("").Key("dbPort").String()
	metricKeys := cfg.Section("").Key("metricKeys").String()
	apiPath := "/api/measures/component?component="
	token := cfg.Section("").Key("token").String()
	tokenEnc := b64.StdEncoding.EncodeToString([]byte(token + ":"))

	ch := make(chan Sonar)

	// for r := range repos {

	// }

	for i := 0; i < len(repos); i += chunkSize {
		end := i + chunkSize

		if end > len(repos) {
			end = len(repos)
		}

		divided = append(divided, repos[i:end])
	}

	for _, chunk := range divided {
		for _, repo := range chunk {
			key := cfg.Section(repo).Key("key").String()
			componentType := cfg.Section(repo).Key("type").String()
			url := sonarURL + apiPath + key + "&metricKeys=" + metricKeys
			go GetMeasures(url, componentType, tokenEnc, ch)
		}
	}

	var projects []Sonar

	for range repos {
		projects = append(projects, <-ch)
	}

	ClientWrite(databaseURL, database, projects)

	elapsed := time.Since(start)
	log.Printf("Took %s", elapsed)

}
