package main
import (
	"os"
	"log"
	"strconv"
)

var (
	API_KEYS_FILE_NAME = func()string{check_args();return os.Args[1]}()
	NUMBER_OF_CONCURRENT_CART_HANDLERS = func()int64{
		check_args();
		if ret, err := strconv.Atoi(os.Args[2]); err == nil && ret > 0 {
			return int64(ret)
		} else {
			log.Fatal("number_of_concurrent_tweetcart_handlers must be a number > 0")
		}
		panic("Should not get here")
		}()
	WEBHOOK_DOMAIN_NAME = func()string{check_args();return os.Args[3]}()
	WEBHOOK_ENV_NAME = func()string{check_args();return os.Args[4]}()
	LOGFILE_NAME = func()string{if len(os.Args) > 5 {return os.Args[5]} else {return ""}}()

)

func check_args() {
	if len(os.Args) < 5 {
		log.Fatalf("Usage: %v file_containing_api_keys number_of_concurrent_tweetcart_handlers webhook_domain_name webook_env_name [log_file_name]", os.Args[0])
	}
}