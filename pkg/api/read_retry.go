package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"time"
)

// Replay only content reads. Posting a comment, changing account state or
// sending an SMS must never be repeated because its response body was empty.
func replayableRead(a *API) bool {
	if a.Route != RouteContent || a.Action == "multi_video_model" {
		return false
	}
	if a.Method == GET {
		return true
	}
	switch a.Action {
	case "category_landpage", "book_pack_fields", "multi_video_detail", "video_detail",
		"read_progress_get", "comment_list", "reply_list":
		return true
	}
	return false
}

func retryableReadResult(res *Result, err error) bool {
	if err == nil {
		return res != nil && res.Envelope.Code == 110001
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var empty *EmptyResponseError
	var network net.Error
	var business *BizError
	return errors.As(err, &empty) || errors.As(err, &network) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		(errors.As(err, &business) && business.HTTPCode >= 500)
}

func (s *Session) sendReadRequest(ctx context.Context, a *API, query url.Values, body []byte, contentType string, compression bool, creds sessionCredentials) (*Result, error) {
	attempts := 1
	if replayableRead(a) {
		attempts = 3
	}
	var res *Result
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if attempt > 0 {
			current := s.credentials()
			if current.generation != creds.generation {
				return res, err
			}
			creds = current
		}
		res, err = s.sendRequest(ctx, a, query, body, contentType, compression, creds)
		if attempt+1 == attempts || !retryableReadResult(res, err) || s.credentials().generation != creds.generation {
			return res, err
		}
		timer := time.NewTimer((150 * time.Millisecond) << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return res, err
}
