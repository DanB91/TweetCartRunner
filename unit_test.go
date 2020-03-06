//Copyright (C) 2020 Daniel Bokser.  See LICENSE file for license
package main

import (
	"bytes"
	"image/gif"
	"io/ioutil"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"twitter"
)

func test_assert_eq(expected, actual interface{}, msg string, t *testing.T) {
	if actual != expected {
		switch ex := expected.(type) {
		case string:
			ac := actual.(string)
			for i, j := 0, 0; i < len(ex) && j < len(ac); i, j = i+1, j+1 {
				if ex[i] != ac[j] {
					t.Errorf("%v -- Actual: %v, Expected: %v", msg, actual, expected)
					t.Error("Differ at ", i, " Expected: ", ex[i:], " Actual: ", ac[j:])
					return
				}
			}
		default:
			t.Errorf("%v -- Actual: %v, Expected: %v", msg, actual, expected)
		}
	}
}

func test_assert_less(l, r int, msg string, t *testing.T) {
	if l >= r {
		t.Errorf("%v -- %v is not less than %v", msg, l, l)
	}
}
func test_assert_no_err(err error, msg string, t *testing.T) {
	if err != nil {
		t.Errorf("%v -- Error: %v", msg, err)
	}
}

func BenchmarkGenerateGIF(b *testing.B) {
	cart_contents :=
		`
    p={129,1,140,12,7}
    for i=1,#p do
    pal(i,p[i],1)
    end
    a={}
    ::_::
    cls()
    for i=0,9 do
    add(a,{x=64,y=128,n=1-rnd(2),m=-3-rnd(2+2*sin(t()/2)),s=1+rnd(3)})
    end
    for k,e in pairs(a) do
    e.m+=.1
    e.s-=rnd(.05)
    e.x+=e.n
    e.y+=e.m
    circfill(e.x,e.y,1,e.s+2)
    if(e.y>130)del(a,e)
    end
    flip()goto _
    `
	for n := 0; n < b.N; n += 1 {
		run_pico8_and_generate_gif(cart_contents, strconv.Itoa(n))
	}
}
func BenchmarkTokenize(b *testing.B) {
	cart := `r=rnd f=flr m={-1,1}
	srand(2)
	s='@tweetcartrunner'l={}      
	
	for i=1,#s do
	add(l,{
	vx=(r(4)+1)*m[f(r(2))+1],
	vy=(r(4)+1)*m[f(r(2))+1],
	x=i*4,y=0,
	l=sub(s,i,i),
	c=r(15)+1})
	end
	::_:: cls()
	for l in all(l) do
	l.x+=l.vx l.y+=l.vy
	if(l.x<0 or l.x>128)l.vx*=-1
	if(l.y<0 or l.y>128)l.vy*=-1
	? l.l,l.x,l.y,l.c
	end
	flip()
	goto _`
	for n := 0; n < b.N; n++ {
		tokenize(cart)
	}
}
func BenchmarkDivideCartUpIntoTweets(b *testing.B) {
	cart := `r=rnd f=flr m={-1,1}
	srand(2)
	s='@tweetcartrunner'l={}      
	
	for i=1,#s do
	add(l,{
	vx=(r(4)+1)*m[f(r(2))+1],
	vy=(r(4)+1)*m[f(r(2))+1],
	x=i*4,y=0,
	l=sub(s,i,i),
	c=r(15)+1})
	end
	::_:: cls()
	for l in all(l) do
	l.x+=l.vx l.y+=l.vy
	if(l.x<0 or l.x>128)l.vx*=-1
	if(l.y<0 or l.y>128)l.vy*=-1
	? l.l,l.x,l.y,l.c
	end
	flip()
	goto _`
	for n := 0; n < b.N; n++ {
		divide_cart_up_into_tweets(cart, "test_user")
	}
}
func TestDivideCartUpIntoTweets(t *testing.T) {
	tweets := divide_cart_up_into_tweets("?\"hello!\"", "test_user")
	test_assert_eq(1, len(tweets), "Should only be 1 tweet", t)
	test_assert_eq("@test_user ?\"hello!\"", tweets[0], "Unexpected tweet contents", t)

	tweets = divide_cart_up_into_tweets(`r=rnd f=flr m={-1,1}
srand(2)
s='@tweetcartrunner'l={}      

for i=1,#s do           
      
add(l,{
vx=(r(4)+1)*m[f(r(2))+1],
vy=(r(4)+1)*m[f(r(2))+1],
x=i*4,y=0,
l=sub(s,i,i),
c=r(15)+1})
end
::_:: cls()
for l in all(l) do
l.x+=l.vx l.y+=l.vy
if(l.x<0 or l.x>128)l.vx*=-1
if(l.y<0 or l.y>128)l.vy*=-1
? l.l,l.x,l.y,l.c
end
flip()
goto _`, "test_user")
	test_assert_eq(2, len(tweets), "Should be 2 tweets", t)

	test_assert_eq(`@test_user r=rnd f=flr m={-1,1}
srand(2)
s='@tweetcartrunner'l={}

for i=1,#s do

add(l,{
vx=(r(4)+1)*m[f(r(2))+1],
vy=(r(4)+1)*m[f(r(2))+1],
x=i*4,y=0,
l=sub(s,i,i),
c=r(15)+1})
end
::_:: cls()
for l in all(l) do
l.x+=l.vx l.y+=l.vy
--1/2`, tweets[0], "First tweet is incorrect", t)
	test_assert_less(len([]rune(tweets[0])), 240, "Tweet too long", t)

	test_assert_eq(`@test_user if(l.x<0 or l.x>128)l.vx*=-1
if(l.y<0 or l.y>128)l.vy*=-1
? l.l,l.x,l.y,l.c
end
flip()
goto _ --2/2`, tweets[1], "Second tweet is wrong", t)
	test_assert_less(len([]rune(tweets[0])), 240, "Tweet too long", t)

}

func TestSanitizeTweet(t *testing.T) {
	//no erasure since its right after a quote
	//also replaces non-ASCII tick with ascii quote
	{
		expected := "s=' @TweetCartRunner '\n  "
		indices := []twitter.Indices{twitter.Indices{4, 20}}
		actual, err := sanitize_tweet_text("s=‘ @TweetCartRunner ’\n  #include test  ", indices)

		test_assert_no_err(err, "error sanitizing tweet", t)
		test_assert_eq(expected, actual, "", t)
	}
	//no erasure since its in quotes
	{
		expected := "s=\"@TweetCartRunner\""
		indices := []twitter.Indices{twitter.Indices{3, 19}}
		actual, err := sanitize_tweet_text("s=\"@TweetCartRunner\"", indices)
		test_assert_no_err(err, "error sanitizing tweet", t)
		test_assert_eq(expected, actual, "", t)
	}
	//no erasure since its right after a quote
	{
		expected := "s='@TweetCartRunner\""
		indices := []twitter.Indices{twitter.Indices{3, 19}}
		actual, err := sanitize_tweet_text("s='@TweetCartRunner\"", indices)
		test_assert_no_err(err, "error sanitizing tweet", t)
		test_assert_eq(expected, actual, "", t)
	}

	//erases the second mention
	{
		expected := "s=\"@TweetCartRunner\""
		indices := []twitter.Indices{twitter.Indices{3, 19}, twitter.Indices{20, 36}}
		actual, err := sanitize_tweet_text("s=\"@TweetCartRunner\"@TweetCartRunner", indices)
		test_assert_no_err(err, "error sanitizing tweet", t)
		test_assert_eq(expected, actual, "", t)
	}

}
func commonGenerateGif(cart_contents string, t *testing.T) {

	image_data, err := run_pico8_and_generate_gif(cart_contents, strconv.Itoa(rand.Int()))
	test_assert_no_err(err, "Could not generate GIF", t)

	bytes_reader := bytes.NewReader(image_data)
	image, err := gif.DecodeAll(bytes_reader)
	test_assert_no_err(err, "GIF data not valid", t)
	max_size := 15 * (1 << 20) //15MB
	if len(image_data) > max_size {
		t.Errorf("GIF data is too large -- Max: %v, Actual: %v", max_size, len(image_data))
	}

	max_frames := 350
	if len(image_data) > max_size {
		t.Errorf("Too many frames for GIF -- Max: %v, Actual: %v", max_frames, len(image.Image))
	}

	//print("Bytes size: ", len(image_data), " frame count: ", len(image.Image), " width: ", image.Config.Width, " height: ", image.Config.Height, "\n")
}

func TestGenerateGif(t *testing.T) {
	long_running_cart :=
		`
    p={129,1,140,12,7}
    for i=1,#p do
    pal(i,p[i],1)
    end
    a={}
    ::_::
    cls()
    for i=0,9 do
    add(a,{x=64,y=128,n=1-rnd(2),m=-3-rnd(2+2*sin(t()/2)),s=1+rnd(3)})
    end
    for k,e in pairs(a) do
    e.m+=.1
    e.s-=rnd(.05)
    e.x+=e.n
    e.y+=e.m
    circfill(e.x,e.y,1,e.s+2)
    if(e.y>130)del(a,e)
    end
    flip()goto _
    `
	commonGenerateGif(long_running_cart, t)

	quick_cart := "print('hello!')"
	commonGenerateGif(quick_cart, t)

	large_gif := `
    ::_::
    for i=0,10000 do
    pset(rnd(128),rnd(128),flr(rnd(16)))
    end
    flip()
    goto _`
	commonGenerateGif(large_gif, t)
}

//type TweetCartRunnerPersistentState struct {
//last_tweet_id int64
//tweet_ids_in_progress map[int64]bool
//}

func TestPersistThread(t *testing.T) {
	test_file := "test_persist.json"
	os.Remove(test_file)
	state := load_persistent_state_file(test_file)
	test_assert_eq(int64(0), state.LastTweetID, "Initial tweet should be 0", t)

	tweet_ids_in_progress := make(chan int64)
	processed_tweet_ids := make(chan int64)
	status_channel := make(chan int)

	//TODO need some sort of sync channel
	go perstisting_thread(tweet_ids_in_progress, processed_tweet_ids, state, status_channel, test_file)

	current_tweet_id := int64(123)
	tweet_ids_in_progress <- current_tweet_id

	status := <-status_channel
	test_assert_eq(PTS_STATE_PERSISTED, status, "", t)

	test_assert_eq(int64(0), state.LastTweetID, "Should not be updated for in progress tweets", t)
	v, ok := state.TweetIDsInProgress[current_tweet_id]
	test_assert_eq(true, ok, "Should be recorded as in progress", t)
	test_assert_eq(true, v, "Should be recorded as in progress", t)

	file_contents, err := ioutil.ReadFile(test_file)
	test_assert_eq(nil, err, "persist file should exist", t)
	test_assert_eq(`{"LastTweetID":0,"TweetIDsInProgress":{"123":true}}`, string(file_contents), "Bad file contents in persist file", t)

	processed_tweet_ids <- current_tweet_id

	status = <-status_channel
	test_assert_eq(PTS_STATE_PERSISTED, status, "", t)

	test_assert_eq(current_tweet_id, state.LastTweetID, "Should be updated for processed tweets", t)
	v, ok = state.TweetIDsInProgress[current_tweet_id]
	test_assert_eq(false, ok, "Should no longer be in progress", t)
	test_assert_eq(false, v, "Should no longer be in progress", t)

	file_contents, err = ioutil.ReadFile(test_file)
	test_assert_eq(nil, err, "persist file should exist", t)
	test_assert_eq(`{"LastTweetID":123,"TweetIDsInProgress":{}}`, string(file_contents), "Bad file contents in persist file", t)
}
