/*
 Copyright 2021 The CloudEvents Authors
 SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"
)

func main() {
	ctx := cloudevents.ContextWithTarget(context.Background(), "http://ad6803b0d00c14e4ab8683ddaf5338ef-2564348c1172fcc3.elb.us-east-1.amazonaws.com")

	p, err := cloudevents.NewHTTP()
	if err != nil {
		log.Fatalf("failed to create protocol: %s", err.Error())
	}

	c, err := cloudevents.NewClient(p, cloudevents.WithTimeNow(), cloudevents.WithUUIDs())
	if err != nil {
		log.Fatalf("failed to create client, %v", err)
	}

	for i := 0; i < 10; i++ {
		e := cloudevents.NewEvent()
		e.SetType("com.cloudevents.sample.sent")
		e.SetSource("https://github.com/cloudevents/sdk-go/v2/samples/httpb/sender")
		_ = e.SetData(cloudevents.ApplicationJSON, map[string]interface{}{
			"id":      i,
			"message": "Hello, World!",
		})

		res := c.Send(ctx, e)
		if cloudevents.IsUndelivered(res) {
			log.Printf("Failed to send: %v", res)
		} else {
			var httpResult *cehttp.Result
			if cloudevents.ResultAs(res, &httpResult) {
				var err error
				if httpResult.StatusCode != http.StatusOK {
					err = fmt.Errorf(httpResult.Format, httpResult.Args...)
				}
				log.Printf("Sent %d with status code %d, error: %v", i, httpResult.StatusCode, err)
			} else {
				log.Printf("Send did not return an HTTP response: %s", res)
			}
		}
	}
}
