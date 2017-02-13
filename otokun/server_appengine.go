// Copyright 2016 LINE Corporation
//
// LINE Corporation licenses this file to you under the Apache License,
// version 2.0 (the "License"); you may not use this file except in compliance
// with the License. You may obtain a copy of the License at:
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
// License for the specific language governing permissions and limitations
// under the License.

// +build appengine

package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"

	"google.golang.org/appengine"
	aelog "google.golang.org/appengine/log"
	"google.golang.org/appengine/taskqueue"
	"google.golang.org/appengine/urlfetch"

	"github.com/line/line-bot-sdk-go/linebot"
	"github.com/line/line-bot-sdk-go/linebot/httphandler"
)

type item struct {
	qty       float64
	price     float64
	unitPrice float64
	unitLabel string
	label     string
}
type items []item

func (it items) Len() int           { return len(it) }
func (it items) Less(i, j int) bool { return it[i].unitPrice < it[j].unitPrice }
func (it items) Swap(i, j int)      { it[i], it[j] = it[j], it[i] }

func init() {
	handler, err := httphandler.New(
		os.Getenv("CHANNEL_SECRET"),
		os.Getenv("CHANNEL_TOKEN"),
	)
	if err != nil {
		log.Fatal(err)
	}

	handler.HandleEvents(callbackHandleFunc)
	http.Handle("/callback", handler)
	http.HandleFunc("/task", queuedTaskHandleFunc)
}

func callbackHandleFunc(events []*linebot.Event, r *http.Request) {
	ctx := appengine.NewContext(r)
	tasks := make([]*taskqueue.Task, len(events))
	i := 0
	for _, event := range events {
		if event.Type == linebot.EventTypeMessage {
			switch message := event.Message.(type) {
			case *linebot.TextMessage:
				task := taskqueue.NewPOSTTask("/task",
					url.Values{"data": {message.Text}, "replyToken": {event.ReplyToken}})
				//aelog.Infof(ctx, "message.Text=%v", message.Text)
				tasks[i] = task
				i++
			}
		}
	}
	_, err := taskqueue.AddMulti(ctx, tasks, "")
	if err != nil {
		aelog.Errorf(ctx, "%v", err)
		return
	}
}

func queuedTaskHandleFunc(w http.ResponseWriter, r *http.Request) {
	handler, err := httphandler.New(
		os.Getenv("CHANNEL_SECRET"),
		os.Getenv("CHANNEL_TOKEN"),
	)
	if err != nil {
		log.Fatal(err)
	}
	ctx := appengine.NewContext(r)

	bot, err := handler.NewClient(linebot.WithHTTPClient(urlfetch.Client(ctx)))
	if err != nil {
		aelog.Errorf(ctx, "%v", err)
		return
	}

	data := r.FormValue("data")
	if data == "" {
		aelog.Errorf(ctx, "No data")
		return
	}

	replyToken := r.FormValue("replyToken")
	if replyToken == "" {
		aelog.Errorf(ctx, "No replyToken")
		return
	}

	re := regexp.MustCompile(`\s*(\d+)([^\d\s]+)?\s*(\d+)([^\d\s]+)?`)
	results := re.FindAllStringSubmatch(data, -1)

	it := make(items, len(results))
	// 最安を探す
	for i, result := range results {
		d1, _ := strconv.ParseFloat(result[1], 64)
		d3, _ := strconv.ParseFloat(result[3], 64)
		//logf(c, "%d,%s|%d,%s\n", d1, result[2], d3, result[4])
		if result[2] == "円" {
			it[i].qty = d3
			it[i].price = d1
			it[i].label = result[3] + result[4]
			it[i].unitLabel = result[4]
		} else {
			it[i].qty = d1
			it[i].price = d3
			it[i].label = result[1] + result[2]
			it[i].unitLabel = result[2]
		}
		it[i].unitPrice = it[i].price / it[i].qty
	}

	sort.Sort(it)
	//logf(c, "%#v\n", it)
	msg := "エラー"
	if len(it) == 2 {
		// 2個の場合「500mlが3mlオトク」
		deltaQty := it[0].qty - it[0].price/it[1].unitPrice
		var format string
		if deltaQty >= 1 {
			format = "%sの方が%.0f%sオトク"
		} else if deltaQty >= 0.1 {
			format = "%sの方が%.1f%sオトク"
		} else {
			format = "%sの方が%.3f%sオトク"
		}
		msg = fmt.Sprintf(format, it[0].label, deltaQty, it[0].unitLabel)
	} else {
		msg = it[0].label + "が一番オトク"
		for i := 1; i < len(it); i++ {
			deltaQty := it[i].price/it[0].unitPrice - it[i].qty
			msg += fmt.Sprintf("、%sは%.1f%s損", it[i].label, deltaQty, it[i].unitLabel)
		}
		// 3個以上なら「500mlが一番オトク、350mlは3ml損、750mlは25ml損」
	}

	m := linebot.NewTextMessage(msg)
	if _, err = bot.ReplyMessage(replyToken, m).WithContext(ctx).Do(); err != nil {
		aelog.Errorf(ctx, "ReplayMessage: %v", err)
		return
	}

	w.WriteHeader(200)
}
