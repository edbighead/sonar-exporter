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

// JenkinsJob represents response object from jenkins rest api.
type JenkinsJob struct {
	Result    string
	Timestamp int64
	URL       string
	Key       string
}

// StringToFloat converts sonar api response strings to float
func StringToFloat(metric, value string) float64 {
	var alert = map[string]float64{
		"OK":    1,
		"WARN":  0.5,
		"ERROR": 0,
	}

	var status = map[string]float64{
		"SUCCESS":  1,
		"UNSTABLE": 0.5,
		"ERROR":    0,
	}

	var val float64
	var err error

	if metric == "alert_status" {
		val = alert[value]
	} else if metric == "status" {
		val = status[value]
	} else {
		val, err = strconv.ParseFloat(value, 64)
		if err != nil {
			log.Printf("Unable to convert string to float")
		}
	}

	return val
}

// SinceJob returns number of hours since given unix timestamp (13 digits) from jenkins response
func SinceJob(t int64) float64 {
	UTCfromUnixNano := time.Unix(int64(t/1000), 0)
	duration := time.Since(UTCfromUnixNano)
	return duration.Hours()
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

// GetJob sends mapped JenkinsJob object from response to channel
func GetJob(repo, url, token string, ch chan JenkinsJob) {

	token = "Basic " + token
	req, err := http.NewRequest("GET", url+"/lastBuild/api/json?tree=result,timestamp,url", nil)

	req.Header.Add("Authorization", token)

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error on response.\n[ERROR] -", err)
	}

	body, _ := ioutil.ReadAll(resp.Body)

	var j JenkinsJob

	json.Unmarshal(body, &j)
	j.Key = repo

	ch <- j

}

// ClientWrite inserts measures into influx database
func ClientWrite(databaseURL, databse string, projects []Sonar, jobs []JenkinsJob) {
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

	for _, j := range jobs {
		key := j.Key
		statusValue := StringToFloat("status", j.Result)
		todayValue := SinceJob(j.Timestamp)
		urlValue := j.URL
		status := client.Point{
			Measurement: "jenkins_status",
			Tags: map[string]string{
				"project_key": key,
			},
			Fields: map[string]interface{}{
				"val": statusValue,
			},
			Time:      time.Now(),
			Precision: "n",
		}

		url := client.Point{
			Measurement: "jenkins_url",
			Tags: map[string]string{
				"project_key": key,
			},
			Fields: map[string]interface{}{
				"val": urlValue,
			},
			Time:      time.Now(),
			Precision: "n",
		}

		since := client.Point{
			Measurement: "jenkins_since",
			Tags: map[string]string{
				"project_key": key,
			},
			Fields: map[string]interface{}{
				"val": todayValue,
			},
			Time:      time.Now(),
			Precision: "n",
		}

		pts = append(pts, status, url, since)

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
	cfg, err := ini.Load("../config.ini")
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
	jenkinsToken := cfg.Section("").Key("jenkins_token").String()
	jenkinsUser := cfg.Section("").Key("jenkins_user").String()
	jenkinsTokenEnc := b64.StdEncoding.EncodeToString([]byte(jenkinsUser + ":" + jenkinsToken))

	ch := make(chan Sonar)
	jch := make(chan JenkinsJob)

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
			jenkinsURL := cfg.Section(repo).Key("job").String()
			go GetMeasures(url, componentType, tokenEnc, ch)
			go GetJob(key, jenkinsURL, jenkinsTokenEnc, jch)
		}
	}

	var projects []Sonar
	var jobs []JenkinsJob

	for range repos {
		projects = append(projects, <-ch)
		jobs = append(jobs, <-jch)
	}

	ClientWrite(databaseURL, database, projects, jobs)

	elapsed := time.Since(start)
	log.Printf("Took %s", elapsed)

}
