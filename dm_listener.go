package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"twitter"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sync/semaphore"
)

const (
	WEBHOOK_PATH = "/webhook"
	DM_ID_PREFIX = "dm"
)

var (
	TWITTER_ACCOUNT_ACTIVITY = "https://api.twitter.com/1.1/account_activity/all/" + WEBHOOK_ENV_NAME
	WEBHOOK_URL              = "https://" + WEBHOOK_DOMAIN_NAME + WEBHOOK_PATH
)

type DirectMessage struct {
	SenderId    string `json:"sender_id"`
	MessageData struct {
		Text string `json:"text"`
	} `json:"message_data"`
}
type DirectMessageEvent struct {
	Type    string        `json:"type"`
	Message DirectMessage `json:"message_create"`
	Id      string        `json:"id"`
}
type User struct {
	Id         string `json:"id"`
	ScreenName string `json:"screen_name"`
}
type DirectMessageEvents struct {
	Events    []DirectMessageEvent `json:"direct_message_events"`
	ForUserId string               `json:"for_user_id"`
	Users     map[string]User      `json:"users"`
}

type DMCart struct {
	dm_id   string
	dm_text string
	sender  User
}

type WebhookHandler struct {
	consumer_secret            []byte
	ctx                        context.Context
	program_handling_semaphore *semaphore.Weighted
	twitter_client             *twitter.Client
	this_user                  *twitter.User
}

func (handler *WebhookHandler) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	buf := bytes.Buffer{}
	if req.ContentLength > 0 {
		buf.Grow(int(req.ContentLength))
	}

	_, err := buf.ReadFrom(req.Body)
	if err != nil {
		log.Println("Error reading webhook request: ", err)
		return
	}
	if token_slice, ok := req.URL.Query()["crc_token"]; ok {
		if len(token_slice) < 1 {
			log.Println("crc_token has no elements!")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		token := []byte(token_slice[0])
		hash := hmac.New(sha256.New, handler.consumer_secret)
		if _, err := hash.Write(token); err != nil {
			log.Println("Could not hash crc_token!  Error:", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp_str := fmt.Sprintf("{\"response_token\": \"sha256=%v\"}", base64.StdEncoding.EncodeToString(hash.Sum(nil)))
		writer.Header().Set("Content-Type", "application/json")
		writer.Write([]byte(resp_str))
		return
	}

	var dm_events DirectMessageEvents
	if err = json.Unmarshal(buf.Bytes(), &dm_events); err != nil {
		return
	}
	for _, dm_event := range dm_events.Events {
		if dm_event.Type != "message_create" {
			log.Println("Got dm event that was not of type message_create. Skipping... Type: ", dm_event.Type)
			continue
		}
		sender, ok := dm_events.Users[dm_event.Message.SenderId]
		if !ok {
			log.Printf("User %v not found in dm event %+v.  Skipping", dm_event.Message.SenderId, dm_event)
			continue
		}

		if sender.ScreenName == handler.this_user.ScreenName {
			continue
		}
		var (
			dm_text string = dm_event.Message.MessageData.Text
			dm_id   string = DM_ID_PREFIX + dm_event.Id
		)
		//TODO use a channel to send this data over so we can have a bounded number of threads
		go handle_dm(dm_id, dm_text, sender, handler)
	}

	writer.WriteHeader(http.StatusOK)
}

//returns true if successful, else false
func send_dm(dm_text string, to User, twitter_client *twitter.Client) {
	//TODO: loop that reads from channel?
	api_func := func() (interface{}, error) {
		new_dm_params := twitter.DirectMessageEventsNewParams{
			Event: &twitter.DirectMessageEvent{Type: "message_create",
				Message: &twitter.DirectMessageEventMessage{
					Target: &twitter.DirectMessageTarget{RecipientID: to.Id},
					Data:   &twitter.DirectMessageData{Text: dm_text}}}}
		new_event, _, err := twitter_client.DirectMessages.EventsNew(&new_dm_params)
		return new_event, err
	}
	_, err := execute_twitter_api(api_func, "", false)
	if err != nil {
		log.Printf("Failed to send DM \"%v\" to user %v. Reason: %v", dm_text, to.ScreenName, err)
	}
}

func assert(condition bool, message string) {
	if !condition {
		panic(message)
	}
}

type Token struct {
	token_runes    []rune
	new_line_count int
}

func tokenize(str string) []Token {
	tokens := make([]Token, 0, 240)
	str = strings.TrimSpace(str)
	runes := []rune(str)
	t := Token{token_runes: make([]rune, 0, 240),
		new_line_count: 0}
	saw_space := false
	for _, r := range runes {
		if unicode.IsSpace(r) {
			if r == '\n' {
				t.new_line_count++
			}
			saw_space = true
		} else {
			if saw_space {
				tokens = append(tokens, t)
				t.new_line_count = 0
				t.token_runes = make([]rune, 0, 16)
			}
			t.token_runes = append(t.token_runes, r)
			saw_space = false
		}
	}
	tokens = append(tokens, t)

	return tokens
}

func divide_cart_up_into_tweets(cart, my_screen_name string) []string {
	const MAX_TWEET_CHARS = 240
	tag_str_len := 1 + len(my_screen_name) + 1 // @-sign, screen name, space
	if len(cart)+tag_str_len <= MAX_TWEET_CHARS {
		tweet := fmt.Sprintf("@%v %v", my_screen_name, cart)
		//TODO remove!
		assert(len(tweet) <= MAX_TWEET_CHARS, "Tweet too large")
		return []string{tweet}
	}
	tweet_counter_len := 2 + 5 // double-dash, double digit counter
	tweets := make([]string, 0, 9)

	tokens := tokenize(cart)
	current_tweet := "@" + my_screen_name + " "
	for _, token := range tokens {
		if utf8.RuneCountInString(current_tweet)+
			len(token.token_runes)+
			tweet_counter_len > MAX_TWEET_CHARS {

			current_tweet += fmt.Sprintf("--%v/", len(tweets)+1)
			tweets = append(tweets, current_tweet)
			current_tweet = "@" + my_screen_name + " "
		}
		current_tweet += string(token.token_runes)
		if token.new_line_count > 0 {
			current_tweet += strings.Repeat("\n", token.new_line_count)
		} else {
			current_tweet += " "
		}
	}
	current_tweet += fmt.Sprintf("--%v/", len(tweets)+1)
	tweets = append(tweets, current_tweet)
	for i := 0; i < len(tweets); i++ {
		tweets[i] += strconv.Itoa(len(tweets))
	}

	return tweets

}
func handle_dm(dm_id, dm_text string, sender User, handler *WebhookHandler) {
	go send_dm("Your code is being run.  I will DM you once it's finished!", sender, handler.twitter_client)

	if err := handler.program_handling_semaphore.Acquire(handler.ctx, 1); err != nil {
		log.Print("Failed to acquire semaphore.  Reason: ", err)
		return
	}
	//TODO
	// handler.dm_ids_in_process <- dm_id
	defer handler.program_handling_semaphore.Release(1)
	// defer func() { handler.processed_dm_ids <- dm_id }()

	sanitized_text, err := sanitize_tweet_text(dm_text, nil)
	if err != nil {
		log.Printf("Failed to sanitize DM. Dropping... Reason: %v", err)
		return
	}
	gif_data, err := run_pico8_and_generate_gif(sanitized_text, dm_id)
	if err != nil {
		msg := `I was unable to generate the GIF of your program. Possible reasons:

- There is a syntax error in your tweetcart.
- There is an infinite loop and flip() is not being called.
- flip() is overridden.`
		send_dm(msg, sender, handler.twitter_client)
		log.Printf("Failed generate for DM gif. Dropping... Reason: %v", err)
		return
	}

	gif_id, err := upload_gif(gif_data, handler.twitter_client)
	if err != nil {
		log.Print("Could not upload GIF! Error: ", err)
		send_dm("An internal error has occurred.  Please try back later.", sender, handler.twitter_client)
		return
	}
	api_func := func() (interface{}, error) {
		status_update_params := &twitter.StatusUpdateParams{
			Status:             "",
			InReplyToStatusID:  0,
			PossiblySensitive:  twitter.Bool(false),
			Lat:                nil,
			Long:               nil,
			PlaceID:            "",
			DisplayCoordinates: twitter.Bool(false),
			TrimUser:           twitter.Bool(true),
			MediaIds:           []int64{gif_id},
			TweetMode:          "extended",
		}
		tweet, _, err := handler.twitter_client.Statuses.Update("By @"+sender.ScreenName, status_update_params)
		return tweet, err
	}
	tweet_int, err := execute_twitter_api(api_func, "Error posting GIF tweet of DM!", false)
	if err != nil {
		send_dm("There was an error posting your program.  Please try back later.", sender, handler.twitter_client)
		return
	}
	tweet := tweet_int.(*twitter.Tweet)

	cart_tweets := divide_cart_up_into_tweets(sanitized_text, handler.this_user.ScreenName)
	for _, cart_tweet := range cart_tweets {
		api_func := func() (interface{}, error) {
			status_update_params := &twitter.StatusUpdateParams{
				Status:             "",
				InReplyToStatusID:  tweet.ID,
				PossiblySensitive:  twitter.Bool(false),
				Lat:                nil,
				Long:               nil,
				PlaceID:            "",
				DisplayCoordinates: twitter.Bool(false),
				TrimUser:           twitter.Bool(true),
				MediaIds:           nil,
				TweetMode:          "extended",
			}
			tweet, _, err := handler.twitter_client.Statuses.Update(cart_tweet, status_update_params)
			return tweet, err
		}
		_, err := execute_twitter_api(api_func, "Error posting cart from DM!", false)
		if err != nil {
			send_dm(fmt.Sprintf("I have successfully ran your program! But there was an error posting your source code. I posted your program here: https://twitter.com/%v/status/%v",
				sender.Id, tweet.IDStr), sender, handler.twitter_client)
			return
		}
	}

	send_dm(fmt.Sprintf("I have successfully ran your program!  I posted it here along with the source code: https://twitter.com/%v/status/%v",
		sender.Id, tweet.IDStr), sender, handler.twitter_client)

}
func register_webhook(http_client *http.Client) {
	tw_url := TWITTER_ACCOUNT_ACTIVITY + "/webhooks.json?url=" + url.QueryEscape(WEBHOOK_URL)
	req, err := http.NewRequest("POST", tw_url, nil)
	if err != nil {
		log.Fatal("Error registering webhook: ", err)
	}
	resp, err := http_client.Do(req)
	if err != nil {
		log.Fatal("Error registering webhook: ", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		buf := bytes.Buffer{}
		if resp.ContentLength > 0 {
			buf.Grow(int(resp.ContentLength))
		}
		_, err = buf.ReadFrom(resp.Body)
		if err != nil {
			log.Fatal("Error reading registration response: ", err)
		}

		log.Fatalf("Could not register webhook. Status Code: %v Reason: %v", resp.StatusCode, buf.String())
	}
}
func subscribe_to_messages(http_client *http.Client) {

	tw_url := TWITTER_ACCOUNT_ACTIVITY + "/subscriptions.json"
	req, err := http.NewRequest("POST", tw_url, nil)
	if err != nil {
		log.Fatal("Error subscribing to messages: ", err)
	}
	resp, err := http_client.Do(req)
	if err != nil {
		log.Fatal("Error subscribing to messages: ", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		buf := bytes.Buffer{}
		if resp.ContentLength > 0 {
			buf.Grow(int(resp.ContentLength))
		}
		_, err = buf.ReadFrom(resp.Body)
		if err != nil {
			log.Fatal("Error reading subscribing response: ", err)
		}
		log.Fatal("Error subscribing to messages: ", buf.String())
	}

}

type Webhook struct {
	Id    string `json:"id"`
	Valid bool   `json:"valid"`
}

func delete_all_current_webhooks(http_client *http.Client) {

	tw_url := TWITTER_ACCOUNT_ACTIVITY + "/webhooks.json"

	req, err := http.NewRequest("GET", tw_url, nil)
	if err != nil {
		log.Fatal("Error getting webhooks: ", err)
	}
	resp, err := http_client.Do(req)
	if err != nil {
		log.Fatal("Error registering webhook: ", err)
	}
	defer resp.Body.Close()
	buf := bytes.Buffer{}
	if resp.ContentLength > 0 {
		buf.Grow(int(resp.ContentLength))
	}
	_, err = buf.ReadFrom(resp.Body)
	if err != nil {
		log.Fatal("Error reading registration response: ", err)
	}
	var webhooks []Webhook
	if err = json.Unmarshal(buf.Bytes(), &webhooks); err != nil {
		log.Fatal("Error registering webhook.  Response: ", buf.String())
	}

	for _, webhook := range webhooks {
		tw_url := TWITTER_ACCOUNT_ACTIVITY + "/webhooks/" + webhook.Id + ".json"
		req, err := http.NewRequest("DELETE", tw_url, nil)
		if err != nil {
			log.Fatal("Error getting webhooks: ", err)
		}
		resp, err := http_client.Do(req)
		if err != nil {
			log.Fatal("Error registering webhook: ", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 204 {
			log.Fatal("Error deleting webhook: ", webhook.Id)
		}
		log.Printf("Deleted webhook %v", webhook.Id)
	}
}
func wait_for_webhook_to_come_up() {
	if _, err := net.Dial("tcp", os.Args[3]+":443"); err != nil {
		log.Fatal("Webhook did not come up!  Reason: ", err)
	}
}
func register_welcome_message(twitter_client *twitter.Client) {
	log.Print("Registering welcome message...")
	welcome_message_params := twitter.DirectMessageWelcomeMessageNewParams{
		MessageData: twitter.DirectMessageData{Text: `Welcome! 240 characters not enough?  You've come to the right place!
		Simply DM me your PICO-8 code and I'll run it and will post the tweet of the following:
			- An 8-second GIF of it running.
			- Tagging you as the author.
			- Reply to this tweet with the source code.`,
		},
		Name: "Default Message"}
	msg, _, err := twitter_client.DirectMessages.WelcomeMessageNew(&welcome_message_params)

	if err != nil {
		log.Fatal("Error posting welcome message! Reason: ", err)
	}
	_, _, err = twitter_client.DirectMessages.WelcomeMessageRuleNew(msg.ID)
	if err != nil {
		log.Fatal("Error posting welcome message! Reason: ", err)
	}

	log.Print("Done!")

}
func init_dm_listener(consumer_secret string, http_client *http.Client,
	twitter_client *twitter.Client, this_user *twitter.User,
	ctx context.Context, program_handling_semaphore *semaphore.Weighted) {
	list, _, err := twitter_client.DirectMessages.WelcomeMessageList(nil)
	if err != nil {
		log.Fatal("Error listing welcome messages! Reason: ", err)
	}
	if len(list.WelcomeMessages) == 0 {
		register_welcome_message(twitter_client)

	} else if len(list.WelcomeMessages) > 1 {
		for _, msg := range list.WelcomeMessages {
			_, err := twitter_client.DirectMessages.WelcomeMessageDestroy(msg.ID)

			if err != nil {
				log.Fatal("Error deleting welcome message! Reason: ", err)
			}
		}
		register_welcome_message(twitter_client)
	}

	handler := WebhookHandler{
		consumer_secret:            []byte(consumer_secret),
		ctx:                        ctx,
		program_handling_semaphore: program_handling_semaphore,
		twitter_client:             twitter_client,
		this_user:                  this_user,
	}

	mux := http.NewServeMux()
	mux.Handle(WEBHOOK_PATH, &handler)

	listener, err := net.Listen("tcp", ":12345")
	cfg := &tls.Config{
		MinVersion:               tls.VersionTLS12,
		CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_256_GCM_SHA384,

			//recommended from twitter docs
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
			tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
		},
	}
	srv := &http.Server{
		Addr:         ":12345",
		Handler:      mux,
		TLSConfig:    cfg,
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0),
	}
	if err != nil {
		log.Fatal("Error listening on port for webhook: ", err)
	}
	go func() { log.Fatal(srv.ServeTLS(listener, "tls/server.crt", "tls/server.key")) }()
	//wait_for_webhook_to_come_up()
	delete_all_current_webhooks(http_client)

	log.Println("Registering webhook...")
	register_webhook(http_client)
	subscribe_to_messages(http_client)
	log.Println("Done!")

	log.Printf("Ready to listen for DMs!")

	//TODO: remove!
	wait := make(chan struct{})
	<-wait
}
