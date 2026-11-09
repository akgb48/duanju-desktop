package app

import (
	"duanju-desktop/pkg/hgapi"
	"encoding/json"
	"errors"

	"duanju-desktop/pkg/model"
)

// 本文件覆盖 bridge.js 的「评论 / 互动」两组绑定。

// ---------------------------------------------------------------- 评论（13）

// Comments 拉取分集评论列表；整剧剧评由 Reviews 读取。
func (a *App) Comments(seriesID, videoID, cursor string) (*model.CommentPage, error) {
	return a.getComments(seriesID, videoID, 4, cursor)
}

// Danmaku uses the video's actual duration and the server's next time window.
func (a *App) Danmaku(seriesID, videoID string, offsetMs int64, durationMs int) (*model.CommentPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().Danmaku(ctx, seriesID, videoID, offsetMs, int64(durationMs))
}
func (a *App) Replies(seriesID, videoID, commentID, cursor string) (*model.CommentPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().Replies(ctx, seriesID, videoID, commentID, cursor, false)
}
func (a *App) Reviews(seriesID, videoID, cursor string) (*model.CommentPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().Reviews(ctx, seriesID, cursor)
}
func (a *App) ReviewReplies(seriesID, reviewID, cursor string) (*model.CommentPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().Replies(ctx, seriesID, seriesID, reviewID, cursor, true)
}

func (a *App) publishSocial(post hgapi.SocialPost) (*model.Comment, error) {
	if err := a.ensureLoggedIn(); err != nil {
		return nil, err
	}
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	item, err := svc.Client().PublishSocial(ctx, post)
	if err != nil {
		return nil, err
	}
	if u := svc.CurrentUser(); u != nil {
		item.UserID = u.UserID
		item.Nickname = u.Nickname
		item.Avatar = u.Avatar
	}
	return item, nil
}
func decodeCommentImages(raw string) ([]model.CommentImage, error) {
	if raw == "" {
		return nil, nil
	}
	var images []model.CommentImage
	if err := json.Unmarshal([]byte(raw), &images); err != nil {
		return nil, errors.New("评论图片格式错误")
	}
	if len(images) > 9 {
		return nil, errors.New("最多添加 9 张图片")
	}
	return images, nil
}
func (a *App) SendComment(seriesID, videoID, text, presetID, imagesJSON string) (*model.Comment, error) {
	images, err := decodeCommentImages(imagesJSON)
	if err != nil {
		return nil, err
	}
	return a.publishSocial(hgapi.SocialPost{SeriesID: seriesID, VideoID: videoID, Text: text, PresetID: presetID, Kind: 4, Images: images})
}
func (a *App) SendDanmaku(seriesID, videoID, text string, offsetMs int64, presetID string) (*model.Comment, error) {
	return a.publishSocial(hgapi.SocialPost{SeriesID: seriesID, VideoID: videoID, Text: text, OffsetMs: offsetMs, PresetID: presetID, Kind: 20})
}
func (a *App) SendReply(seriesID, videoID, commentID, replyID, userID, text string, danmaku bool, offsetMs int64, imagesJSON string) (*model.Comment, error) {
	images, err := decodeCommentImages(imagesJSON)
	if err != nil {
		return nil, err
	}
	kind := 4
	if danmaku {
		kind = 20
	}
	return a.publishSocial(hgapi.SocialPost{SeriesID: seriesID, VideoID: videoID, CommentID: commentID, ReplyID: replyID, ReplyUserID: userID, Text: text, Kind: kind, OffsetMs: offsetMs, Images: images})
}
func (a *App) SendReview(seriesID, text string, score int) (*model.Comment, error) {
	return a.publishSocial(hgapi.SocialPost{SeriesID: seriesID, Text: text, Kind: 2, Score: score})
}
func (a *App) SendReviewReply(seriesID, reviewID, replyID, text string) (*model.Comment, error) {
	return a.publishSocial(hgapi.SocialPost{SeriesID: seriesID, CommentID: reviewID, ReplyID: replyID, Text: text, Kind: 2})
}

// DeleteComment 删除自己的评论。
func (a *App) DeleteComment(commentID string, dataType int) error {
	sess, err := a.session()
	if err != nil {
		return err
	}
	if commentID == "" {
		return errors.New("缺少评论 ID")
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	_, err = sess.CallJSON(ctx, "comment_del", map[string]any{
		"comment_id": commentID,
		"data_type":  dataType,
	})
	return err
}

// LikeComment 给评论点赞/取消。
func (a *App) LikeComment(commentID string, liked bool) error {
	sess, err := a.session()
	if err != nil {
		return err
	}
	action := 1
	if !liked {
		action = 0
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	_, err = sess.CallJSON(ctx, "comment_do_action", map[string]any{
		"comment_id": commentID,
		"action":     action,
	})
	return err
}

// LikeDanmaku 给弹幕点赞/取消。
func (a *App) LikeDanmaku(commentID string, liked bool) error {
	return a.LikeComment(commentID, liked)
}

// ---------------------------------------------------------------- 互动（12）

// LikeReview 给剧评点赞/取消。
func (a *App) LikeReview(reviewID string, liked bool) error {
	return a.LikeComment(reviewID, liked)
}

// Likes 分页读取账号点赞的视频。
func (a *App) Likes(cursor, kind string) (*model.FeedPage, error) {
	if err := a.ensureLoggedIn(); err != nil {
		return nil, err
	}
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().Likes(ctx, cursor, kind)
}

// RemoveLikes 批量取消点赞。
func (a *App) RemoveLikes(refs []hgapi.LikeRef) (*hgapi.RemoveLikesResult, error) {
	out := &hgapi.RemoveLikesResult{Removed: []hgapi.LikeRef{}}
	if err := a.ensureLoggedIn(); err != nil {
		return nil, err
	}
	for _, ref := range refs {
		if err := a.SetLike(ref.SeriesID, ref.VideoID, false); err != nil {
			out.Failed++
			out.Error = err.Error()
		} else {
			out.Removed = append(out.Removed, ref)
		}
	}
	return out, nil
}

// SetLike 设置单条点赞状态。
func (a *App) SetLike(seriesID, videoID string, liked bool) error {
	if err := a.ensureLoggedIn(); err != nil {
		return err
	}
	svc, err := a.service()
	if err != nil {
		return err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().SetLike(ctx, seriesID, videoID, liked)
}

// Emoticons 拉取表情包列表。
func (a *App) Emoticons(seriesID, cursor string) (any, error) {
	sess, err := a.session()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	res, err := sess.CallJSON(ctx, "emoticon_list", map[string]any{
		"book_id": seriesID,
		"cursor":  cursor,
	})
	if err != nil {
		return nil, err
	}
	var out any
	_ = res.Decode(&out)
	return out, nil
}

// CollectEmoticon 收藏表情。
func (a *App) CollectEmoticon(emoticonID string) error {
	sess, err := a.session()
	if err != nil {
		return err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	_, err = sess.CallJSON(ctx, "emoticon_collect", map[string]any{"id": emoticonID})
	return err
}

// UploadPicture 以 base64 数据上传评论配图，返回图片 URL。
func (a *App) UploadPicture(base64Data string) (string, error) {
	svc, err := a.service()
	if err != nil {
		return "", err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.UploadPicture(ctx, base64Data)
}

// UploadLocal 上传本地文件作为配图。
func (a *App) UploadLocal(path string) (string, error) {
	return "", errors.New("请在前端读取文件后调用 UploadPicture")
}

// Favorites 分页返回追剧书架。
func (a *App) Favorites(cursor string, reset bool) ([]model.Series, error) {
	return a.favoritesList()
}

// SetFavorite 设置追剧状态。
func (a *App) SetFavorite(seriesID string, on bool) (bool, error) {
	if seriesID == "" {
		return false, errors.New("缺少剧集 ID")
	}
	memMu.Lock()
	if on {
		s, ok := memSeries[seriesID]
		if !ok {
			s = model.Series{ID: seriesID, Title: seriesID}
		}
		memFavorites[seriesID] = s
	} else {
		delete(memFavorites, seriesID)
	}
	memMu.Unlock()
	return on, a.persistLocal()
}

// RemoveFavorites 批量取消追剧。
func (a *App) RemoveFavorites(ids []string) error {
	for _, id := range ids {
		memMu.Lock()
		delete(memFavorites, id)
		memMu.Unlock()
	}
	return a.persistLocal()
}

// FollowUpdates 返回关注动态。
func (a *App) FollowUpdates(cursor string) (*model.FeedPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Feed(ctx, 205, cursor)
}

// ---------------------------------------------------------------- 关注与预约（5）

// IsFollowingUpdate 查询是否关注某剧更新。
func (a *App) IsFollowingUpdate(seriesID string) (bool, error) {
	return a.isFavorite(seriesID), nil
}

// SetFollowUpdate 设置关注更新。
func (a *App) SetFollowUpdate(seriesID string, on bool) (bool, error) {
	return a.SetFavorite(seriesID, on)
}

// FollowUser 关注某个用户。
func (a *App) FollowUser(userID string, on bool) error {
	sess, err := a.session()
	if err != nil {
		return err
	}
	action := 1
	if !on {
		action = 0
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	_, err = sess.CallJSON(ctx, "article_do_action", map[string]any{
		"target_id": userID,
		"action":    action,
		"type":      2,
	})
	return err
}

func (a *App) reservationClient() (*hgapi.Client, error) {
	if err := a.ensureLoggedIn(); err != nil {
		return nil, err
	}
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	return svc.Client(), nil
}

// Reservations 分页读取当前账号的已上线或待上线预约。
func (a *App) Reservations(online bool, cursor string) (*hgapi.ReservationPage, error) {
	client, err := a.reservationClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return client.Reservations(ctx, online, cursor)
}

// SetReservation 设置预约状态。
func (a *App) SetReservation(seriesID string, on bool) (bool, error) {
	client, err := a.reservationClient()
	if err != nil {
		return false, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	if err := client.SetReservation(ctx, seriesID, on); err != nil {
		return false, err
	}
	return on, nil
}

// DeleteReservations 批量删除指定页签里的预约记录。
func (a *App) DeleteReservations(ids []string, online bool) error {
	client, err := a.reservationClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return client.DeleteReservations(ctx, ids, online)
}

// Calendar returns the upstream release schedule for a date and cursor.
func (a *App) Calendar(date, cursor string) (*hgapi.CalendarPage, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return svc.Client().CalendarPage(ctx, date, cursor)
}
