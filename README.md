# Tweet Cart Runner Bot

This is the source code used by the twitter bot, [@TweetCartRunner](https://twitter.com/TweetCartRunner).  If you choose to run your own Tweetcart Runner Bot with this code or derivatives publicly, please publicly credit this repo.


# Requirements
---
- golang compiler -- Tested on 1.13, but should work on 1.11 and up.
- Linux -- Tested on Ubuntu 18.04.
- PICO-8 -- Tested on 0.1.12C

# Compilation and Setup
---
Compiliation has been tested on Go 1.13, but should work for 1.11 and up. I have only tested this on Linux, but in theory should work all OSs that support Go and PICO-8.  The only caveat right now is that your PICO-8 binary has to be in a folder called `pico-8-linux` or `pico-8-rpi` and the executable has to be called pico8. Running this on macOS would probably require code changes due to the .app folder structure. Here are steps to compile and setup the bot:


- `git clone` or download this repo.  You should have a directory called `tweet_cart_runner`
- `cd tweet_cart_runner`
- Run the following `go get` commands:

  ```
  go get github.com/dghubble/oauth1
  go get github.com/dghubble/sling
  go get github.com/cenkalti/backoff
  go get golang.org/x/sync/semaphore
  ```
- Copy your PICO-8 Linux folder, `pico-8-linux` to the current directory.  If you are on a Raspberry PI, you can copy the `pico-8-rpi` folder.  
- Run `go build` and it should build successfully
- Run `go test` and all tests should pass
- You will need to create a Twitter developer account and create an app.
- You will need the `consumer key`, `consumer secret`, `token` and `token secret` of you app.  Place these 4 API strings in the a new plain text file called `keys.txt` in the following order:

```
consume key
consumer secret
token
token secret
```
- Now the bot is ready to run.  See the [Usage](#usage) section.

# Usage
---
`./twitter_pico8 file_containing_api_keys number_of_concurrent_tweetcart_handlers [log_file_name]`

- `file_containing_api_keys` -- This is the text file that contains your Twitter app's API keys.  This is the `keys.txt` created in the [Compilation and Setup](#compilation-and-setup]) section.

- `number_of_concurrent_tweetcart_handlers` -- This specifies the number of tweets this bot can handle at once.  Keep in mind that this means up to this many PICO-8 instances can be running on your machine at once.
  You'd want to make sure your machine is up to the task of running this many instances at once.

- `[log_file_name]` -- Optional. You can specify the name of a log file to log some debug output (specifically any call to `log.Print()` and others).  If not specified, stdout is used.

### Examples of Usage
- `./twitter_pico8 keys.txt 8 tweet_cart_runner.log` -- This will run the bot with API keys located in the `keys.txt`, can handle up to 8 tweets (PICO-8 instances) at a time, and log debug output to a file called `tweet_cart_runner.log`.

- `./twitter_pico8 keys.txt 8` -- This will run the bot with API keys located in the `keys.txt`, can handle up to 8 tweets (PICO-8 instances) at a time, and log debug output to stdout.

### Persistent State

There will be times where you might want to bring down the bot for upgrading or other maintenance, but you don't want to miss any tweets that come in during that downtime. That's where persistent state comes in! This bot keeps a file called `persistent_state.json` which keeps track of 2 things:

- `LastTweetID` -- This is the ID of the last successfully processed tweet.
- `TweetIDsInProgress` -- A list of IDs of tweets that are being processed.  Try to make sure this list is empty before bringing down the bot (that is, if you are controlling when it goes down).

When the bot is brought up, it will check for this file.  If it exists, it will do the following:
- First attempt to process any tweets in `TweetIDsInProgress` (i.e. any tweets that were being processed when the bot went down).
- Then process any mentions that that came in after the value in `LastTweetID`

If the `persistent_state.json` doesn't exist the bot will just start up with out checking for any previous mentions.

# Contact
---
If you have any questions or concerns related to this software feel free to DM me on Twitter @dbokser91 or email me at tweetcartrunner@yahoo.com and I'll try to respond in a timely manner.
