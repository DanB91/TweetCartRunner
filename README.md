# Tweet Cart Runner Bot

This is the source code used by the twitter bot, [@TweetCartRunner](https://twitter.com/TweetCartRunner).  If you choose to run your own Tweetcart Runner Bot with this code or derivatives publicly, please publicly credit this repo.

This bot listens for tweets and DMs that contain PICO-8 code, runs the code and then, if everything was successful, uploads a GIF of the program running.

# Requirements

- A valid HTTPS certificate.  Self-signed may work, but have not tested this. This is required by Twitter's DM API, which is unfortunate, to put it nicely.

- A domain name (Free domain-names by no-ip.org may work, but have not tried this.). Also required by the DM API.

- Able to listen to and open up port 443, which requires sudo access and possibly port forwarding. You can whitelist `199.16.156.0/22` and `199.59.148.0/22` (Twitter's IPs) in your firewall.

- Twitter developer account with a "Dev Environment" set up on developer.twitter.com and DM permissions enabled.

- golang compiler -- Tested on 1.13, but should work on 1.11 and up.

- Either:
  - Linux -- Tested on Ubuntu 18.04, or
  - macOS -- Tested on Catalina.

- PICO-8 -- Tested on 0.1.12C.

# Compilation and Setup

Compiliation has been tested on Go 1.13, but should work for 1.11 and up.  Here are steps to compile and setup the bot:


- `git clone` or download this repo.  You should have a directory called `tweet_cart_runner`
- `cd tweet_cart_runner`
- Run the following `go get` commands:

  ```
  go get github.com/dghubble/oauth1
  go get github.com/dghubble/sling
  go get github.com/cenkalti/backoff
  go get golang.org/x/sync/semaphore
  ```
- If you are on Linux:
  - Copy your PICO-8 Linux folder, `pico-8-linux` to the current directory.  If you are on a Raspberry PI, you can copy the `pico-8-rpi` folder.
- Else, if you are on Mac:
  - Copy PICO-8.app to the current directory.
- Run `go build` and it should build successfully
- Run `go test` and all tests should pass
- You will need to create a Twitter developer account and create an app.
- You will need the `consumer key`, `consumer secret`, `token` and `token secret` of your app. Make sure they have read, write and DM permissions.  Place these 4 API strings in the a new plain text file called `keys.txt` in the following order:

```
consume key
consumer secret
token
token secret
```

- Create a "Dev Environment" in your app.  You will need the name of this environment as a command-line argument for this bot.

- Make a directory in the current directory called `tls`.

- Generate your HTTPS certificate and key and name them `server.crt` and `server.key`, respectively.  

- Copy these files into the `tls` directory.

- Make sure port 443 is open.

- Now the bot is ready to run.  See the [Usage](#usage) section.

# Usage

`./TweetCartRunner file_containing_api_keys number_of_concurrent_tweetcart_handlers webhook_domain_name webook_env_name [log_file_name]`

- `file_containing_api_keys` -- This is the text file that contains your Twitter app's API keys.  This is the `keys.txt` created in the [Compilation and Setup](#compilation-and-setup]) section.

- `number_of_concurrent_tweetcart_handlers` -- This specifies the number of tweets this bot can handle at once.  Keep in mind that this means up to this many PICO-8 instances can be running on your machine at once.
  You'd want to make sure your machine is up to the task of running this many instances at once.

- `webhook_domain_name` -- Your domain name which is what Twitter will use to connect to send over DMs.

- `webook_env_name` -- The name of the "Dev Environment" you have set up on developer.twitter.com.

- `[log_file_name]` -- Optional. You can specify the name of a log file to log some debug output (specifically any call to `log.Print()` and others).  If not specified, stdout is used.

### Examples of Usage
- `./twitter_pico8 keys.txt 8 my_domain.com my_dev_env tweet_cart_runner.log` -- This will run the bot with API keys located in the `keys.txt`, can handle up to 8 tweets (PICO-8 instances) at a time, and log debug output to a file called `tweet_cart_runner.log`.  It will tell the Twitter API to connect to this instance at `https://my_domain.com` using the `my_dev_env` "Dev Environment".

- `./twitter_pico8 keys.txt 8 my_domain.com my_dev_env` -- This will run the bot with API keys located in the `keys.txt`, can handle up to 8 tweets (PICO-8 instances) at a time, and log debug output to stdout. It will tell the Twitter API to connect to this instance at `https://my_domain.com` using the `my_dev_env` "Dev Environment".

### Persistent State

There will be times where you might want to bring down the bot for upgrading or other maintenance, but you don't want to miss any tweets that come in during that downtime. That's where persistent state comes in! This bot keeps a file called `persistent_state.json` which keeps track of 4 things:

- `LastTweetID` -- This is the ID of the last successfully processed tweet.
- `TweetIDsInProgress` -- A list of IDs of tweets that are being processed.  Try to make sure this list is empty before bringing down the bot (that is, if you are controlling when it goes down).
- `LastDMID` -- This is the ID of the last successfully processed DM.
- `DMsInProgress` -- A list of DMs that are being processed right now.  Try to make sure this list is empty before bringing down the bot (that is, if you are controlling when it goes down).

When the bot is brought up, it will check for this file.  If it exists, it will do the following:
- First attempt to process any tweets in `TweetIDsInProgress` (i.e. any tweets that were being processed when the bot went down).
- Then process any mentions that that came in after the value in `LastTweetID`
- Then process any DMs in `DMsInProgress`.
- Then process any DMs since the ID in `LastDMID`.

If the `persistent_state.json` doesn't exist the bot will just start up with out checking for any previous mentions.

# Contact

If you have any questions or concerns related to this software feel free to DM me on Twitter @dbokser91 or email me at tweetcartrunner@yahoo.com and I'll try to respond in a timely manner.
