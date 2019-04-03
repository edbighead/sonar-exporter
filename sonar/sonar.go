package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	b64 "encoding/base64"

	ini "gopkg.in/ini.v1"
)

type Sonar struct {
	Component struct {
		Key      string
		Name     string
		Measures []Measure
	}
}

type Measure struct {
	Metric string
	Value  string
}

func GetMeasures(url, token string, ch chan Sonar) {

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

	ch <- s

}

func main() {
	cfg, err := ini.Load("config.ini")
	if err != nil {
		fmt.Printf("Fail to read file: %v", err)
		os.Exit(1)
	}

	repos := cfg.SectionStrings()[1:]
	sonarURL := cfg.Section("").Key("sonarUrl").String()
	metricKeys := cfg.Section("").Key("metricKeys").String()
	apiPath := "/api/measures/component?component="
	token := cfg.Section("").Key("token").String()
	tokenEnc := b64.StdEncoding.EncodeToString([]byte(token + ":"))

	ch := make(chan Sonar)

	for r := range repos {
		key := cfg.Section(repos[r]).Key("key").String()
		url := sonarURL + apiPath + key + "&metricKeys=" + metricKeys
		go GetMeasures(url, tokenEnc, ch)
	}

	var projects []Sonar

	for range repos {
		projects = append(projects, <-ch)
	}

	fmt.Print(projects)

}
