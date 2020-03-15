//Copyright (C) 2020 Daniel Bokser.  See LICENSE file for license
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"twitter"

	"github.com/dghubble/oauth1"
	"golang.org/x/sync/semaphore"
)

type TweetCartRunnerPersistentState struct {
	LastTweetID        int64
	TweetIDsInProgress map[int64]bool
	//TODO
}

const (
	//PTS -> "persisted thread state"
	PTS_STATE_PERSISTED = iota
)

type CartTweet struct {
	tweet_id        int64
	parent_tweet_id int64
}

func perstisting_thread(tweet_ids_in_progress, processed_tweet_ids <-chan int64, persistent_state *TweetCartRunnerPersistentState, status_channel chan<- int, file_name string) {
	needs_persist := false
	for {
		if needs_persist {
			select {
			case tweet_id := <-tweet_ids_in_progress:
				persistent_state.TweetIDsInProgress[tweet_id] = true
				needs_persist = true
			case tweet_id := <-processed_tweet_ids:
				if tweet_id > persistent_state.LastTweetID {
					persistent_state.LastTweetID = tweet_id
				}
				delete(persistent_state.TweetIDsInProgress, tweet_id)
				needs_persist = true
			default:
				//persist file
				bytes, err := json.Marshal(persistent_state)
				if err != nil {
					log.Print("Cannot serialize persistent state.  No state will persiste anymore. Reason: ", err)
					return
				}
				err = ioutil.WriteFile(file_name, bytes, 0600)
				if err != nil {
					log.Print("Cannot write ", file_name, " No state will persiste anymore. Reason: ", err)
					return

				}
				if status_channel != nil {
					status_channel <- PTS_STATE_PERSISTED
				}
				needs_persist = false
			}
		} else {
			select {
			case tweet_id := <-tweet_ids_in_progress:

				persistent_state.TweetIDsInProgress[tweet_id] = true
				needs_persist = true
			case tweet_id := <-processed_tweet_ids:
				if tweet_id > persistent_state.LastTweetID {
					persistent_state.LastTweetID = tweet_id
				}
				delete(persistent_state.TweetIDsInProgress, tweet_id)
				needs_persist = true
			}

		}

	}
}

func load_persistent_state_file(file_name string) *TweetCartRunnerPersistentState {
	var persistent_state TweetCartRunnerPersistentState
	state_json, err := ioutil.ReadFile(file_name)
	if err == nil && len(state_json) > 0 {
		err = json.Unmarshal(state_json, &persistent_state)
		if err != nil {
			log.Fatal("Could not read json from perisistent_state.json. Exiting... Reason: ", err)
		}
	} else {
		persistent_state.TweetIDsInProgress = make(map[int64]bool)
	}

	return &persistent_state
}

func process_missed_tweets(tc *twitter.Client, persistent_state *TweetCartRunnerPersistentState,
	cart_tweet_channel chan CartTweet) {
	log.Print("Loading missed tweets...")
	cart_tweet := CartTweet{}
	for tweet_id, _ := range persistent_state.TweetIDsInProgress {
		cart_tweet.tweet_id = tweet_id
		cart_tweet.parent_tweet_id = tweet_id
		cart_tweet_channel <- cart_tweet
	}
	if persistent_state.LastTweetID == 0 {
		log.Print("Done!")
		return
	}

	const buffer_size = 20
	total_loaded_tweets := 0
	last_tweet_id := persistent_state.LastTweetID
	for {
		api_func := func() (interface{}, error) {
			log.Print("Since id: ", last_tweet_id)
			timeline_params := &twitter.MentionTimelineParams{
				Count:              buffer_size,
				SinceID:            last_tweet_id,
				MaxID:              0,
				TrimUser:           twitter.Bool(false),
				ContributorDetails: twitter.Bool(false),
				IncludeEntities:    twitter.Bool(true),
				TweetMode:          "extended",
			}
			tweets, _, err := tc.Timelines.MentionTimeline(timeline_params)
			return tweets, err
		}

		tweets_int, err := execute_twitter_api(api_func, "Cannot retrieve mentions sent before bring up.  Exiting...", true)
		if err != nil {
			//should never get here
			return
		}
		tweets := tweets_int.([]twitter.Tweet)
		if len(tweets) == 0 {
			log.Print("Attempted to load ", total_loaded_tweets, " tweets")
			return
		}
		for _, tweet := range tweets {
			if _, does_contain := persistent_state.TweetIDsInProgress[tweet.ID]; does_contain {
				if tweet.ID > last_tweet_id {
					last_tweet_id = tweet.ID
				}
				continue
			}
			var tweet_id int64
			if tweet.InReplyToStatusID != 0 && tweet.InReplyToUserID == tweet.User.ID {
				tweet_id = tweet.InReplyToStatusID
			} else {
				tweet_id = tweet.ID
			}

			cart_tweet.tweet_id = tweet_id
			cart_tweet.parent_tweet_id = tweet.ID
			cart_tweet_channel <- cart_tweet

			if tweet.ID > last_tweet_id {
				last_tweet_id = tweet.ID
			}

		}

		total_loaded_tweets += len(tweets)
	}
}
func setup_logging(log_file_name string) *os.File {
	f, err := os.OpenFile(log_file_name, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		log.Print("Could not open log file: ", os.Args[1], ". Defaulting to stdout")
		return nil
	}

	log.SetOutput(f)
	return f
}
func load_keys_file(keys_file_name string) (string, string, string, string) {
	contents, err := ioutil.ReadFile(keys_file_name)
	if err != nil {
		log.Fatal("Could not load keys file: ", os.Args[1], ". Exiting...")
	}

	lines := strings.Split(string(contents), "\n")
	if len(lines) < 4 {
		log.Fatal("Invalid keys file!  Must have 4 lines. Exiting...")
	}
	consumer_key := strings.TrimSpace(lines[0])
	consumer_secret := strings.TrimSpace(lines[1])
	token := strings.TrimSpace(lines[2])
	token_secret := strings.TrimSpace(lines[3])

	return consumer_key, consumer_secret, token, token_secret
}
func run_tweet_cart_thread(cart_tweet_channel chan CartTweet,
	tweet_ids_in_progress_channel chan int64, processed_tweet_ids_channel chan int64,
	twitter_client *twitter.Client,
	goroutine_context context.Context, processing_tweet_semaphore *semaphore.Weighted) {

	for tweet := range cart_tweet_channel {
		if err := processing_tweet_semaphore.Acquire(goroutine_context, 1); err != nil {
			log.Print("Error acquiring semaphore: ", err)
			continue
		}

		go func() {
			tweet_ids_in_progress_channel <- tweet.parent_tweet_id
			handle_tweet(tweet.tweet_id, tweet.parent_tweet_id, twitter_client)
			processed_tweet_ids_channel <- tweet.parent_tweet_id
			processing_tweet_semaphore.Release(1)
		}()
	}

}

func main() {

	if len(LOGFILE_NAME) > 0 {
		if f := setup_logging(LOGFILE_NAME); f != nil {
			defer f.Close()
		}
	}

	conusmer_key, consumer_secret, token_str, token_secret := load_keys_file(API_KEYS_FILE_NAME)

	config := oauth1.NewConfig(conusmer_key, consumer_secret)
	token := oauth1.NewToken(token_str, token_secret)

	goroutine_context := context.Background()
	processing_tweet_semaphore := semaphore.NewWeighted(NUMBER_OF_CONCURRENT_CART_HANDLERS)

	// http_client will automatically authorize http.Request's
	http_client := config.Client(oauth1.NoContext, token)
	twitter_client := twitter.NewClient(http_client)
	//log on
	user_name := ""
	logon_func := func() (interface{}, error) {
		user, _, err := twitter_client.Accounts.VerifyCredentials(nil)
		return user, err
	}
	user_int, _ := execute_twitter_api(logon_func, "Could not log on to twitter", true)
	my_user := user_int.(*twitter.User)
	user_name = my_user.ScreenName
	log.Print("Logged on as ", my_user.ScreenName)

	init_dm_listener(consumer_secret, http_client, twitter_client, my_user, goroutine_context, processing_tweet_semaphore)

	for {

		//now listen for tweets tagging the bot

		filter_params := &twitter.StreamFilterParams{
			FilterLevel: "",

			Follow:        nil,
			Language:      nil,
			Locations:     nil,
			StallWarnings: twitter.Bool(true),
			Track:         []string{"@" + user_name},
		}

		stream, err := twitter_client.Streams.Filter(filter_params)
		if err != nil {
			log.Fatal("Could not get stream.  Reason: ", err)
		}

		persistent_state_file_name := "persistent_state.json"
		persistent_state := load_persistent_state_file(persistent_state_file_name)
		tweet_ids_in_progress_channel := make(chan int64, NUMBER_OF_CONCURRENT_CART_HANDLERS)
		processed_tweet_ids_channel := make(chan int64, NUMBER_OF_CONCURRENT_CART_HANDLERS)

		go perstisting_thread(tweet_ids_in_progress_channel, processed_tweet_ids_channel, persistent_state, nil, persistent_state_file_name)

		cart_tweet_channel := make(chan CartTweet, 256)
		go run_tweet_cart_thread(cart_tweet_channel, tweet_ids_in_progress_channel, processed_tweet_ids_channel,
			twitter_client, goroutine_context, processing_tweet_semaphore)

		process_missed_tweets(twitter_client, persistent_state, cart_tweet_channel)

		cart_tweet := CartTweet{}
		for message := range stream.Messages {
			switch msg := message.(type) {
			case *twitter.Tweet:
				if msg.RetweetedStatus != nil {
					//do not handle retweets
					continue
				}
				if msg.User.IDStr == my_user.IDStr {
					//do not process tweets from myself!
					continue
				}
				var tweet_id int64
				if msg.InReplyToStatusID != 0 && msg.InReplyToUserID == msg.User.ID {
					tweet_id = msg.InReplyToStatusID
				} else {
					tweet_id = msg.ID
				}
				cart_tweet.tweet_id = tweet_id
				cart_tweet.parent_tweet_id = msg.ID
				cart_tweet_channel <- cart_tweet

			default:
				log.Printf("Generic handler -- type: %T -- %v", msg, msg)
			}
		}

		log.Print("Connection lost, retrying login in 30 seconds...")
		time.Sleep(30 * time.Second)
		//log on
		user_int, _ := execute_twitter_api(logon_func, "Could not log on to twitter", true)
		user := user_int.(*twitter.User)
		user_name = user.ScreenName
		log.Print("Relogged on as ", user.ScreenName)
	}

}

func is_retriable_error(err error) bool {
	switch typed_err := err.(type) {
	case twitter.APIError:
		if len(typed_err.Errors) > 1 {
			//if there is more than one error, we shouldn't retry.  Just bail out
			return false
		}
		api_err := typed_err.Errors[0]
		if api_err.Code == 420 || api_err.Code == 429 {
			return true
		}

	default:
		//TODO handle timeouts
		return false
	}

	return false
}

func execute_twitter_api(api func() (interface{}, error), error_msg string, is_fatal bool) (interface{}, error) {
	const sleep_seconds = 20
	var (
		ret interface{}
		err error
	)
	for retry := true; retry; {
		ret, err = api()
		if err != nil {
			if is_retriable_error(err) {
				log.Print(error_msg, " Reason: ", err)
				log.Print("Retrying in ", sleep_seconds, " seconds")
				time.Sleep(sleep_seconds * time.Second)
				continue

			} else if is_fatal {
				log.Fatal(error_msg, "Exiting... Reason: ", err)
			} else {
				if len(error_msg) > 0 {
					log.Print(error_msg, " Reason: ", err)
				}
				return nil, err
			}
		}
		retry = false
	}

	return ret, nil

}

var APPROX_FUNCTION_CALL_REGEX = regexp.MustCompile(`\w*([\w, '"]*)`)

func is_probably_code(tweet string) bool {
	return APPROX_FUNCTION_CALL_REGEX.MatchString(tweet) || strings.Contains(tweet, "=") ||
		strings.Contains(tweet, "?'") || strings.Contains(tweet, "?\"")
}

func upload_gif(gif_data []byte, tc *twitter.Client) (int64, error) {
	api_func := func() (interface{}, error) {
		upload_result, _, err := tc.Media.Upload(gif_data, "image/gif", "tweet_gif")
		return upload_result, err
	}
	upload_result_int, err := execute_twitter_api(api_func, "Error uploading gif", false)
	if err != nil {
		return 0, err
	}
	upload_result := upload_result_int.(*twitter.MediaUploadResult)

	if upload_result.ProcessingInfo != nil {
		log.Print("Upload of gif not finished yet.  Checking again in ", upload_result.ProcessingInfo.CheckAfterSecs, " seconds")
		for retry := true; retry; {
			time.Sleep(time.Duration(upload_result.ProcessingInfo.CheckAfterSecs) * time.Second)
			media_status_result, _, err := tc.Media.Status(upload_result.MediaID)
			if err != nil {
				if is_retriable_error(err) {
					//lets retry again
					continue
				} else {
					return 0, fmt.Errorf("Error checking status of uploaded media. Bailing out... Reason: %v", err)
				}
			}

			switch media_status_result.ProcessingInfo.State {
			case "succeeded":
			case "pending":
				fallthrough
			case "in_progress":
				log.Print("Error checking status of uploaded media: ", err)
				//check status again
				continue
			case "failed":
				return 0, fmt.Errorf("Failed to upload gif. Reason: %v", media_status_result.ProcessingInfo.Error.Message)
			default:
				return 0, fmt.Errorf("Unknown media upload state %v. Bailing out", media_status_result.ProcessingInfo.State)
			}
			retry = false
		}

	}

	return upload_result.MediaID, nil

}

func handle_tweet(tweet_id int64, tweet_id_to_persist int64, tc *twitter.Client) {
	var (
		err   error
		tweet *twitter.Tweet
	)

	api_func := func() (interface{}, error) {
		status_show_params := &twitter.StatusShowParams{
			ID:               tweet_id,
			TrimUser:         twitter.Bool(false),
			IncludeMyRetweet: twitter.Bool(false),
			IncludeEntities:  twitter.Bool(true),
			TweetMode:        "extended",
		}
		tweet, _, err = tc.Statuses.Show(tweet_id, status_show_params)
		return tweet, err
	}
	tweet_int, err := execute_twitter_api(api_func, fmt.Sprintf("Error retrieving tweet ID: %v", tweet_id), false)
	if err != nil {
		return
	}
	tweet = tweet_int.(*twitter.Tweet)
	//log.Print("Tweet full text: ", tweet.FullText)

	var indicies_to_remove []twitter.Indices
	if entities := tweet.Entities; entities != nil {
		indicies_to_remove = make([]twitter.Indices, 0, len(entities.UserMentions))
		for _, user_mention := range entities.UserMentions {
			indicies_to_remove = append(indicies_to_remove, user_mention.Indices)
		}
		sort.Slice(indicies_to_remove, func(i, j int) bool {
			return indicies_to_remove[i][1] < indicies_to_remove[i][0]
		})
	}
	sanitized_tweet := sanitize_tweet_text(tweet.FullText, indicies_to_remove)
	//log.Print("Sanitized tweet: ", sanitized_tweet)

	gif_data, err := run_pico8_and_generate_gif(sanitized_tweet, tweet.IDStr)
	if err != nil {
		if !is_probably_code(sanitized_tweet) {
			return
		}
		log.Print("Error generating gif for cart. Reason: ", err)

		api_func = func() (interface{}, error) {
			status_update_params := &twitter.StatusUpdateParams{
				Status:             "",
				InReplyToStatusID:  tweet_id,
				PossiblySensitive:  twitter.Bool(false),
				Lat:                nil,
				Long:               nil,
				PlaceID:            "",
				DisplayCoordinates: twitter.Bool(false),
				TrimUser:           twitter.Bool(true),
				MediaIds:           nil,
				TweetMode:          "extended",
			}
			status := fmt.Sprintf(`@%v
I was unable to generate the GIF of your tweetcart. Possible reasons:

- There is a syntax error in your tweetcart.
- There is an infinite loop and flip() is not being called.
- flip() is overridden.`, tweet.User.ScreenName)

			tweet, _, err = tc.Statuses.Update(status, status_update_params)
			return tweet, err
		}
		execute_twitter_api(api_func, fmt.Sprintf("Error replying to tweet id: %v", tweet.IDStr), false)
		return
	}

	gif_id, err := upload_gif(gif_data, tc)
	if err != nil {
		log.Print(err)
		return
	}

	api_func = func() (interface{}, error) {
		status_update_params := &twitter.StatusUpdateParams{
			Status:             "",
			InReplyToStatusID:  tweet_id,
			PossiblySensitive:  twitter.Bool(false),
			Lat:                nil,
			Long:               nil,
			PlaceID:            "",
			DisplayCoordinates: twitter.Bool(false),
			TrimUser:           twitter.Bool(true),
			MediaIds:           []int64{gif_id},
			TweetMode:          "extended",
		}
		tweet, _, err = tc.Statuses.Update("@"+tweet.User.ScreenName, status_update_params)
		return tweet, err
	}
	_, err = execute_twitter_api(api_func, fmt.Sprintf("Error replying to tweet id: %v", tweet.IDStr), false)
	if err != nil {
		return
	}
	log.Print("Successfully posted GIF for tweet ", tweet_id)

}

var PICO_8_EXEC_PATH = func() string {
	switch runtime.GOOS {
	case "darwin":
		return "./PICO-8.app/Contents/MacOS/pico8"
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return "./pico-8-linux/pico8"
		case "arm":
			return "./pico-8-rpi/pico8"
		default:
			panic("unsupported arch: " + runtime.GOARCH)
		}
	default:
		panic("unsupported os: " + runtime.GOOS)
	}
}()

func run_pico8_and_generate_gif(sanitized_tweet, tweet_id_str string) ([]byte, error) {
	var (
		output       string
		buf          [256]byte
		done_chan    chan bool = make(chan bool)
		timeout_chan <-chan time.Time
	)
	done_str := tweet_id_str + " done"
	file_contents :=
		fmt.Sprintf(
			`pico-8 cartridge // http://www.pico-8.com
version 18
__lua__
load=nil save=nil
__state_%v__={flip=flip, t=t, extcmd=extcmd, printh=printh, start=t(), did_start_rec=false, count=0}
function flip()
    local state = __state_%v__
    if state.t()-state.start >= 8 then
        state.extcmd('video')
        state.printh('%v')
    end
    state.count+=1
    if state.count == 2 then
        state.extcmd('rec')
        state.did_start_rec = true
    end
    state.flip()
end
%v
function finish()
 local state = __state_%v__
 local start = state.t()
 if not state.did_start_rec then
     while state.t() - start < .5 do
     end
     state.extcmd('rec')
 end
 while state.t() - start < 2 do
 end
 state.extcmd('video')
 state.printh('%v')
end
finish()`,
			tweet_id_str,
			tweet_id_str,
			done_str,
			sanitized_tweet,
			tweet_id_str,
			done_str)
	cart_file_name := tweet_id_str + ".p8"
	err := ioutil.WriteFile(cart_file_name, []byte(file_contents), 0600)
	if err != nil {
		log.Print("Error writing cart file! Reason: ", err)
		return nil, err
	} else {
		//log.Print("Wrote tweet to ", cart_file_name)
	}
	defer func() {
		if err := os.Remove(cart_file_name); err != nil {
			log.Print("Could not delete program ", cart_file_name, ". Reason: ", err)
		} else {
			//log.Print("Deleted ", gif_path, " from desktop")
		}
	}()
	user, err := user.Current()
	pico8_command := exec.Command(PICO_8_EXEC_PATH, "-run", tweet_id_str+".p8")
	stdout, err := pico8_command.StdoutPipe()
	if err != nil {
		log.Print("Error getting stdout from ", PICO_8_EXEC_PATH, "Reason: ", err)
		return nil, err
	}
	err = pico8_command.Start()
	//log.Print(script_name, " output:\n", command_output.String())
	if err != nil {
		log.Print("Error running ", PICO_8_EXEC_PATH, "Reason: ", err)
		return nil, err
	}
	defer pico8_command.Process.Wait()
	defer pico8_command.Process.Kill()
	go func() {
		defer func() { done_chan <- true }()
		for {
			n, err := stdout.Read(buf[:])
			if err != nil {
				if err != io.EOF {
					log.Print("Error occurred reading stdout from pico8.  Error: ", err)
				}
				break
			}
			if n == 0 {
				log.Fatal("Stdout return 0 without error!")
			}
			output += string(buf[:n])
			if strings.Contains(output, done_str) {
				break
			}

		}
	}()
	timeout_chan = time.After(30 * time.Second)
	select {
	case <-done_chan:
	case <-timeout_chan:
		return nil, errors.New("Timed out running cart.  Bailing out...")
	}

	gif_path := user.HomeDir + "/Desktop/" + tweet_id_str + "_0.gif"
	defer func() {
		if err := os.Remove(gif_path); err != nil {
			log.Print("Could not delete GIF ", gif_path, ". Reason: ", err)
		} else {
			//log.Print("Deleted ", gif_path, " from desktop")
		}
	}()
	contents, err := ioutil.ReadFile(gif_path)
	return contents, err
}

var INCLUDE_REGEX = regexp.MustCompile(`(?m)^\s*#include\s\S*`)

//Indices to remove must be sorted
func sanitize_tweet_text(text string, indices_to_remove []twitter.Indices) string {
	sanitized_tweet := ""
	current_indices_index := 0
	var indices *twitter.Indices = nil
	if len(indices_to_remove) > 0 {
		indices = &indices_to_remove[0]
	}
	is_currently_in_string := false
	head_quote := rune(0)

	for i, c := range []rune(text) {
		switch c {
		case '”':
			fallthrough
		case '“':
			c = '"'
		case '’':
			fallthrough
		case '‘':
			c = '\''
		}
		if indices != nil {
			if i >= indices[0] && i < indices[1] && !is_currently_in_string {
				continue
			} else if i >= indices[1] {
				current_indices_index += 1
				if current_indices_index >= len(indices_to_remove) {
					indices = nil
				} else {
					indices = &indices_to_remove[current_indices_index]
				}
			}
		}

		if c == '\'' || c == '"' {
			if is_currently_in_string && head_quote == c {
				is_currently_in_string = false
			} else {
				is_currently_in_string = true
				head_quote = c
			}
		}

		sanitized_tweet += string(c)
	}

	sanitized_tweet = strings.ReplaceAll(sanitized_tweet, "&gt;", ">")
	sanitized_tweet = strings.ReplaceAll(sanitized_tweet, "&lt;", "<")
	sanitized_tweet = strings.ReplaceAll(sanitized_tweet, "&amp;", "&")
	sanitized_tweet = INCLUDE_REGEX.ReplaceAllLiteralString(sanitized_tweet, "")

	sanitized_tweet = strings.TrimSpace(sanitized_tweet)
	if sanitized_tweet[0] == '.' {
		sanitized_tweet = sanitized_tweet[1:]
	}

	return sanitized_tweet
}
