package main

import (
	"log"
	"os"
	"strconv"
)

var (
	API_KEYS_FILE_NAME                 string
	NUMBER_OF_CONCURRENT_CART_HANDLERS int64
	WEBHOOK_DOMAIN_NAME                string
	WEBHOOK_ENV_NAME                   string
	LOGFILE_NAME                       string
	TWITTER_ACCOUNT_ACTIVITY           string
	WEBHOOK_URL                        string
)

func load_args() {
	if len(os.Args) < 5 {
		log.Fatalf("Usage: %v file_containing_api_keys number_of_concurrent_tweetcart_handlers webhook_domain_name webook_env_name [log_file_name]", os.Args[0])
	}

	API_KEYS_FILE_NAME = os.Args[1]
	if num_handlers, err := strconv.Atoi(os.Args[2]); err == nil && num_handlers > 0 {
		NUMBER_OF_CONCURRENT_CART_HANDLERS = int64(num_handlers)
	} else {
		log.Fatal("number_of_concurrent_tweetcart_handlers must be a number > 0")
	}
	WEBHOOK_DOMAIN_NAME = os.Args[3]
	WEBHOOK_ENV_NAME = os.Args[4]
	if len(os.Args) > 5 {
		LOGFILE_NAME = os.Args[5]
	}

	TWITTER_ACCOUNT_ACTIVITY = "https://api.twitter.com/1.1/account_activity/all/" + WEBHOOK_ENV_NAME
	WEBHOOK_URL = "https://" + WEBHOOK_DOMAIN_NAME + WEBHOOK_PATH
}
