module github.com/DanB91/TweetCartRunner

go 1.13

require (
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/dghubble/oauth1 v0.6.0
	github.com/dghubble/sling v1.3.0 // indirect
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	twitter v0.0.0-00010101000000-000000000000
)

replace twitter => ./3rdparty/twitter
