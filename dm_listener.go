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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"twitter"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sync/semaphore"
)

const (
	WEBHOOK_PATH = "/webhook"
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
	DMID   string
	DMText string
	Sender User
}

type DMHanderContext struct {
	consumer_secret            []byte
	goroutine_context          context.Context
	program_handling_semaphore *semaphore.Weighted
	twitter_client             *twitter.Client
	my_user                    *twitter.User
	dm_channel                 chan *DMCart
	dms_in_progress            chan *DMCart
	processed_dm_ids           chan string
}

func (dm_context *DMHanderContext) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
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
		hash := hmac.New(sha256.New, dm_context.consumer_secret)
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

		if sender.ScreenName == dm_context.my_user.ScreenName {
			continue
		}
		dm_cart := &DMCart{}
		dm_cart.DMText = dm_event.Message.MessageData.Text
		dm_cart.DMID = dm_event.Id
		dm_cart.Sender = sender
		dm_context.dm_channel <- dm_cart
	}

	writer.WriteHeader(http.StatusOK)
}

func dm_event_loop(dm_context *DMHanderContext) {
	for dm_cart := range dm_context.dm_channel {
		err := dm_context.program_handling_semaphore.Acquire(dm_context.goroutine_context, 1)
		if err != nil {
			log.Print("Error acquiring semaphore: ", err)
			continue
		}
		tmp_dm_cart := dm_cart
		go func() {
			dm_context.dms_in_progress <- tmp_dm_cart
			handle_dm(tmp_dm_cart.DMID, tmp_dm_cart.DMText, tmp_dm_cart.Sender, dm_context)
			dm_context.processed_dm_ids <- tmp_dm_cart.DMID
			dm_context.program_handling_semaphore.Release(1)
		}()
	}
}
func user_screen_names_from_dms(twitter_client *twitter.Client, dms []twitter.DirectMessageEvent) map[string]string {

	ret := make(map[string]string, len(dms))
	users := make([]twitter.User, 100)
	ids_to_lookup := make([]int64, 0, 100) //max 100 per api call

	for i := 0; i < len(dms); i += 100 {
		max_len := 0
		if len(dms[i:]) > 100 {
			max_len = 100
		} else {
			max_len = len(dms)
		}
		for _, dm := range dms[i : i+max_len] {
			sender_id, err := strconv.Atoi(dm.Message.SenderID)
			if err != nil {
				log.Fatalf("User id is not a number! It is %v. Exiting due to DM %v", dm.Message.SenderID, dm.ID)
			}
			ids_to_lookup = append(ids_to_lookup, int64(sender_id))
		}
		api_func := func() (interface{}, error) {
			lookup_params := &twitter.UserLookupParams{
				UserID:          ids_to_lookup,
				ScreenName:      nil,
				IncludeEntities: twitter.Bool(false),
			}
			user_lookup, _, err := twitter_client.Users.Lookup(lookup_params)
			return user_lookup, err
		}
		user_lookup_int, _ := execute_twitter_api(api_func, "Failed to lookup users for dms", true)
		tmp_users := user_lookup_int.([]twitter.User)
		users = append(users, tmp_users...)
		ids_to_lookup = ids_to_lookup[:0]
	}
	for _, user := range users {
		ret[user.IDStr] = user.ScreenName
	}
	return ret
}
func process_missed_dms(tc *twitter.Client, my_user *twitter.User, persistent_state *TweetCartRunnerPersistentState,
	dm_cart_channel chan *DMCart) {
	log.Print("Loading missed dms...")
	for _, dm_cart := range persistent_state.DMsInProgress {
		dm_cart_channel <- dm_cart
	}
	if persistent_state.LastDMID == 0 {
		log.Print("Done!")
		return
	}

	const buffer_size = 20
	total_loaded_dms := 0
	last_dm_id := persistent_state.LastDMID
	params := &twitter.DirectMessageEventsListParams{Count: 50}
	cursor := ""
	for {
		params.Cursor = cursor
		api_func := func() (interface{}, error) {
			log.Print("Since DM id: ", last_dm_id)

			dms, _, err := tc.DirectMessages.EventsList(params)
			return dms, err
		}

		dms_int, err := execute_twitter_api(api_func, "Cannot dms sent before bring up.  Exiting...", true)
		if err != nil {
			//should never get here
			return
		}
		dms := dms_int.(*twitter.DirectMessageEvents)
		if len(dms.Events) == 0 {
			log.Print("Attempted to load ", total_loaded_dms, " dms")
			return
		}
		user_ids_to_screen_names := user_screen_names_from_dms(tc, dms.Events)
		for _, dm := range dms.Events {
			if dm.ID == strconv.Itoa(int(persistent_state.LastDMID)) {
				log.Print("Attempted to load ", total_loaded_dms, " dms")
				return
			}
			if dm.Type != "message_create" {
				continue
			}
			if dm.Message.SenderID == my_user.IDStr {
				continue
			}
			//TODO potential race condition
			if _, does_contain := persistent_state.DMsInProgress[dm.ID]; does_contain {
				continue
			}
			dm_cart := &DMCart{}
			dm_cart.DMID = dm.ID
			dm_cart.DMText = dm.Message.Data.Text
			dm_cart.Sender.Id = dm.Message.SenderID
			if screen_name, ok := user_ids_to_screen_names[dm.Message.SenderID]; ok {
				dm_cart.Sender.ScreenName = screen_name
			} else {
				log.Printf("User %v ID does not exist. Skipping DM id %v", dm.Message.SenderID, dm.ID)
				continue
			}
			dm_cart_channel <- dm_cart
			total_loaded_dms++

		}

	}
}

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
func send_dm_with_gif(dm_text string, to User, gif_id int64, twitter_client *twitter.Client) {
	//TODO: loop that reads from channel?
	api_func := func() (interface{}, error) {
		new_dm_params := twitter.DirectMessageEventsNewParams{
			Event: &twitter.DirectMessageEvent{Type: "message_create",
				Message: &twitter.DirectMessageEventMessage{
					Target: &twitter.DirectMessageTarget{RecipientID: to.Id},
					Data: &twitter.DirectMessageData{Text: dm_text,
						Attachment: &twitter.DirectMessageDataAttachment{Type: "media",
							Media: twitter.MediaEntity{ID: gif_id}}}}}}
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
func handle_dm(dm_id, dm_text string, sender User, handler *DMHanderContext) {
	sanitized_text := sanitize_tweet_text(dm_text, nil)
	is_notweet := strings.HasPrefix(sanitized_text, "--notweet")
	if is_notweet {
		go send_dm("Your code is being run and will not be tweeted.  I will DM you once it's finished!", sender, handler.twitter_client)
	} else {
		go send_dm("Your code is being run and will be tweeted when finished.  I will DM you once it's finished!", sender, handler.twitter_client)
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
	if !is_notweet {
		gif_id, err := upload_gif(gif_data, handler.twitter_client, "tweet_gif")
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

		cart_tweets := divide_cart_up_into_tweets(sanitized_text, handler.my_user.ScreenName)
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
				send_dm(fmt.Sprintf("I have successfully ran your program! But there was an error posting your source code. I posted your program here. https://twitter.com/%v/status/%v",
					sender.Id, tweet.IDStr), sender, handler.twitter_client)
				return
			}
		}

		send_dm(fmt.Sprintf("I have successfully ran your program!  I posted it here along with the source code. https://twitter.com/%v/status/%v",
			sender.Id, tweet.IDStr), sender, handler.twitter_client)

	} else {
		gif_id, err := upload_gif(gif_data, handler.twitter_client, "dm_gif")
		if err != nil {
			log.Print("Could not upload GIF! Error: ", err)
			send_dm("An internal error has occurred.  Please try back later.", sender, handler.twitter_client)
			return
		}
		send_dm_with_gif("I have successfully ran your program!  Here is the result: ", sender, gif_id, handler.twitter_client)

	}

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
			- Reply to this tweet with the source code.
			
		Want to see how your GIF will look without me tweeting it? Have your code start with the comment: --notweet and I'll DM you the GIF!`,
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
func delete_all_welcome_messages(twitter_client *twitter.Client) {
	//delete all messages
	{
		list, _, err := twitter_client.DirectMessages.WelcomeMessageList(nil)
		if err != nil {
			log.Fatal("Error listing welcome messages! Reason: ", err)
		}
		for _, msg := range list.WelcomeMessages {
			_, err := twitter_client.DirectMessages.WelcomeMessageDestroy(msg.ID)

			if err != nil {
				log.Fatal("Error deleting welcome message! Reason: ", err)
			}
		}
	}
	//delete all message rules
	{
		list, _, err := twitter_client.DirectMessages.WelcomeMessageRuleList(nil)
		if err != nil {
			log.Fatal("Error listing welcome message rules! Reason: ", err)
		}
		for _, rule := range list.WelcomeMessageRules {
			_, err := twitter_client.DirectMessages.WelcomeMessageRuleDestroy(rule.ID)

			if err != nil {
				log.Fatal("Error deleting welcome message rule! Reason: ", err)
			}
		}

	}
}
func dm_id_to_int(dm_id string) int64 {
	if dm_id_num, err := strconv.Atoi(dm_id); err == nil {
		return int64(dm_id_num)
	} else {
		log.Fatal("DM ID is not a number, we need to handle this..., DM ID: ", dm_id)
	}

	panic("Should not get here")
}

func wait_for_server_to_come_up() {
	var err error
	for i := 0; i < 3; i++ {
		timeout := time.Second
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(WEBHOOK_DOMAIN_NAME, "443"), timeout)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		if conn != nil {
			conn.Close()
			return
		}
	}

	log.Fatal("HTTPS server failed to come up. Exiting... Reason: ", err)
}
func init_dm_listener(consumer_secret string, http_client *http.Client,
	twitter_client *twitter.Client, my_user *twitter.User,
	dms_in_progress chan *DMCart, processed_dm_ids chan string,
	persistent_state *TweetCartRunnerPersistentState,
	ctx context.Context, program_handling_semaphore *semaphore.Weighted) {
	delete_all_welcome_messages(twitter_client)
	register_welcome_message(twitter_client)

	dm_context := DMHanderContext{
		consumer_secret:            []byte(consumer_secret),
		goroutine_context:          ctx,
		program_handling_semaphore: program_handling_semaphore,
		twitter_client:             twitter_client,
		my_user:                    my_user,
		dm_channel:                 make(chan *DMCart, 256),
		dms_in_progress:            dms_in_progress,
		processed_dm_ids:           processed_dm_ids,
	}

	go dm_event_loop(&dm_context)

	process_missed_dms(twitter_client, my_user, persistent_state, dm_context.dm_channel)

	mux := http.NewServeMux()
	mux.Handle(WEBHOOK_PATH, &dm_context)

	listener, err := net.Listen("tcp", ":443")
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
		Addr:         ":443",
		Handler:      mux,
		TLSConfig:    cfg,
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0),
	}
	if err != nil {
		log.Fatal("Error listening on port for webhook: ", err)
	}

	//start web server!
	go func() { log.Fatal(srv.ServeTLS(listener, "tls/server.crt", "tls/server.key")) }()

	//delete all webhooks on shutdown
	signal_channel := make(chan os.Signal)
	signal.Notify(signal_channel, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-signal_channel
		log.Printf("Received signal %v.  Deleting all webhooks and then going down...", sig)
		delete_all_current_webhooks(http_client)
		srv.Shutdown(ctx)
	}()

	wait_for_webhook_to_come_up()
	delete_all_current_webhooks(http_client)

	log.Println("Registering webhook...")
	register_webhook(http_client)
	subscribe_to_messages(http_client)
	log.Println("Done!")

	log.Printf("Ready to listen for DMs!")

}
