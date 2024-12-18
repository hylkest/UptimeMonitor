package main

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/joho/godotenv"
)

func loadEnv() {
	err := godotenv.Load()
	if err != nil {
		fmt.Printf(err)
		os.Exit(1)
	}
}


var checkedURLs = make(map[string]bool)
var resetInterval = 5 * time.Minute

func main() {
	loadEnv()

	const slackWebhookURL := os.Getenv("SLACK_WEBHOOK_URL")

	smtpServer := os.Getenv("SMTP_SERVER")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUsername := os.Getenv("SMTP_USERNAME")
	smtpPassword := os.Getenv("SMTP_PASSWORD")
	senderEmail := os.Getenv("SENDER_EMAIL")

	dbUsername := os.Getenv("DB_USERNAME")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")
	dbServer := os.Getenv("DB_SERVER")
	dbPort := os.Getenv("DB_PORT")
	//currentTime := time.Now()

	//timeString := currentTime.Format("2006-01-02 15:04:05")
	sendSlackMessage("MONITOR --> Starting script..")
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", dbUsername, dbPassword, dbServer, dbPort, dbName))
	if err != nil {
		fmt.Printf("Error connecting to the database: %v\n", err)
		sendSlackMessage("WARNING --> Database connection error")
		return
	}
	defer db.Close()
	fmt.Println("Database connected")
	sendSlackMessage("MONITOR --> Database connected \nMONITOR --> Script started")
	websites, err := getWebsiteURLs(db)
	if err != nil {
		fmt.Printf("Error fetching website URLs: %v\n", err)
	}

	for _, url := range websites {
		checkWebsite(url, db)
	}

	//sendSlackMessage(fmt.Sprintf("MONITOR --> Checked all websites. TIME: %s", timeString))

	interval := 600 * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	go func() {
		for {
			time.Sleep(resetInterval)
			resetCheckedURLs()
			//sendSlackMessage("MONITOR --> Reset URL MAP ")
		}
	}()

	for {
		select {
		case <-ticker.C:
			websites, err := getWebsiteURLs(db)
			if err != nil {
				fmt.Printf("Error fetching website URLs: %v\n", err)
				continue
			}
			//sendSlackMessage(fmt.Sprintf("MONITOR --> Checked all websites. TIME: %s", timeString))
			//printMemoryUsage()

			for _, url := range websites {
				if !checkedURLs[url] {
					checkWebsite(url, db)
					checkedURLs[url] = true
				}
			}
		}
	}
}

func resetCheckedURLs() {
	// Clear the checkedURLs map
	checkedURLs = make(map[string]bool)
}

func printMemoryUsage() {
	//var m runtime.MemStats
	//runtime.ReadMemStats(&m)

	//message := fmt.Sprintf("Allocated memory: %d B\nTotal allocated memory: %d B\nHeap allocated memory: %d B", m.Alloc, m.TotalAlloc, m.HeapAlloc)
	//sendSlackMessage(message)
}

func sendSlackMessage(message string) {
	message = strings.ReplaceAll(message, `"`, `\"`)
	payload := `{"text": "` + message + `"}`

	resp, err := http.Post(slackWebhookURL, "application/json", strings.NewReader(payload))
	if err != nil {
		fmt.Printf("Error sending Slack message: %v\n", err)
		return
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("Slack API returned non-OK status: %s\n", resp.Status)
		return
	}
}

func getWebsiteURLs(db *sql.DB) ([]string, error) {
	query := "SELECT website_url FROM websites"

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var websites []string

	for rows.Next() {
		var websiteURL string
		if err := rows.Scan(&websiteURL); err != nil {
			return nil, err
		}
		websites = append(websites, websiteURL)
	}

	return websites, nil
}

func updateWebsiteStatus(db *sql.DB, url string, status string, responseTime time.Duration) {
	query := "UPDATE websites SET website_status = ?, last_updated = DATE_ADD(NOW(), INTERVAL 1 HOUR), response_time = ? WHERE website_url = ?"
	_, err := db.Exec(query, status, responseTime.Seconds(), url)
	if err != nil {
		fmt.Printf("Error updating website status for %s: %v\n", url, err)
	}
}

func saveRespTime(db *sql.DB, url string, responseTime time.Duration) {
	query := "INSERT INTO response_times (website_url, response_time) VALUES (?, ?)"
	_, err := db.Exec(query, url, responseTime.Seconds())
	if err != nil {
		fmt.Printf("Error updating website status for %s: %v\n", url, err)
	}
}

func checkSSL(db *sql.DB, url string) {
	strippedURL := strings.TrimPrefix(url, "https://")
	conn, err := tls.Dial("tcp", strippedURL+":443", nil)
	if err != nil {
		panic("Server doesn't support SSL certificate err: " + err.Error())
	}

	err = conn.VerifyHostname(strippedURL)
	if err != nil {
		panic("Hostname doesn't match with certificate: " + err.Error())
	}
	expiry := conn.ConnectionState().PeerCertificates[0].NotAfter

	issuer := conn.ConnectionState().PeerCertificates[0].Issuer.String()
	expiredssl := expiry.Format(time.RFC850)

	query := "UPDATE websites SET ssl_issuer = ?, ssl_expired_date = ? WHERE website_url = ?"
	_, err = db.Exec(query, issuer, expiredssl, url)
	if err != nil {
		fmt.Printf("Error updating website ssl info for %s: %v\n", url, err)
	}
}

func sendEmail(to, subject, body string) {
	auth := smtp.PlainAuth("", smtpUsername, smtpPassword, smtpServer)
	msg := fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s", to, subject, body)

	err := smtp.SendMail(fmt.Sprintf("%s:%d", smtpServer, smtpPort), auth, senderEmail, []string{to}, []byte(msg))
	if err != nil {
		fmt.Printf("Error sending email: %v\n", err)
	}
}

func checkWebsite(url string, db *sql.DB) {
	startTime := time.Now()
	resp, err := http.Get(url)
	currentTime := time.Now()

	timeString := currentTime.Format("2006-01-02 15:04:05")

	if err != nil {
		if strings.Contains(err.Error(), "net/http: TLS handshake timeout") {
			updateWebsiteStatus(db, url, err.Error(), 0)
			fmt.Println("WEBSITE DOWN --> Error: " + err.Error())
			fmt.Println("Current time:", timeString)
			if err != nil {
				sendSlackMessage(fmt.Sprintf("WARNING: Website %s could be down, please check. Status: %s \n Time: %s", url, err.Error(), timeString))
			}
			return
		} else if strings.Contains(err.Error(), "no such host") {
			updateWebsiteStatus(db, url, err.Error(), 0)
			fmt.Println("WEBSITE DOWN --> Error: " + err.Error())
			fmt.Println("Current time:", timeString)
			if err != nil {
				sendSlackMessage(fmt.Sprintf("WARNING: Website %s could be down. Status: %s \n Time: %s", url, err.Error(), timeString))
			}
			sendEmailToClient(db, url, err.Error())
			return
		} else {
			updateWebsiteStatus(db, url, err.Error(), 0)
			fmt.Println("WEBSITE DOWN --> Error: " + err.Error())
			fmt.Println("Current time:", timeString)

			sendEmailToClient(db, url, err.Error())
			if err != nil {
				sendSlackMessage(fmt.Sprintf("ATTENTION: Website %s is down. Status: %s \n Time: %s", url, err.Error(), timeString))
			}
			return
		}
	}

	defer resp.Body.Close()

	responseTime := time.Since(startTime)

	if resp.StatusCode == http.StatusOK {
		updateWebsiteStatus(db, url, "Up", responseTime)
		saveRespTime(db, url, responseTime)
		//sendSlackMessage(fmt.Sprintf("Website %s is up!\n", url))
		//fmt.Println("RESPONSE TIME: ", responseTime)
		//fmt.Println("Current time:", timeString)
		checkSSL(db, url)
		// whoisDomain(url)

	} else {
		updateWebsiteStatus(db, url, fmt.Sprintf("Down (Status Code: %d)", resp.StatusCode), 0)
		fmt.Printf("Website %s is down. Status code: %d\n", url, resp.StatusCode)

		sendEmailToClient(db, url, fmt.Sprintf("Down (Status Code: %d)", resp.StatusCode))
		if err != nil {
			sendSlackMessage(fmt.Sprintf("WARNING: Website %s is down. Status: %s \n Time: %s", url, err.Error(), timeString))
		}
	}
}

func sendEmailToClient(db *sql.DB, url, status string) {
	clientEmailQuery := "SELECT email FROM users WHERE id = (SELECT client FROM websites WHERE website_url = ?)"
	row := db.QueryRow(clientEmailQuery, url)

	var clientEmail string
	err := row.Scan(&clientEmail)
	if err != nil {
		fmt.Printf("Error getting client email for %s: %v\n", url, err)
		return
	}

	subject := fmt.Sprintf("ALERT!!!: Website %s is Down", url)
	body := fmt.Sprintf("Dear user,\n\nThe website %s is currently down.\n\nStatus:\n %s\n\nPlease check it ASAP", url, status)

	sendEmail(clientEmail, subject, body)
}
