package store

const schemaSQL = `
-- kv：通用键值表，存 UI 偏好、播放器设置等零散配置。
CREATE TABLE IF NOT EXISTS kv (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

-- account：单账号登录态。CHECK(id=1) 强制整表最多一行，
-- 同一时间只允许一个登录账号。
CREATE TABLE IF NOT EXISTS account (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	user_id    TEXT,
	token      TEXT,
	cookie     TEXT,
	device_id  TEXT,
	install_id TEXT,
	cdid       TEXT,
	openudid   TEXT,
	nickname   TEXT,
	avatar     TEXT,
	updated_at INTEGER
);

-- progress：按「剧集 + 集号」记录播放进度，主键即幂等键。
CREATE TABLE IF NOT EXISTS progress (
	series_id   TEXT,
	episode     INTEGER,
	position_ms INTEGER,
	duration_ms INTEGER,
	updated_at  INTEGER,
	PRIMARY KEY (series_id, episode)
);

-- download：离线下载任务。state 为 running/done/failed 等状态机取值，
-- bytes/total 用于进度条，error 保留失败原因原文。
CREATE TABLE IF NOT EXISTS download (
	id            TEXT PRIMARY KEY,
	series_id     TEXT,
	series_title  TEXT,
	episode_index INTEGER,
	video_id      TEXT,
	quality       TEXT,
	state         TEXT,
	bytes         INTEGER,
	total         INTEGER,
	error         TEXT,
	width         INTEGER,
	height        INTEGER,
	duration      INTEGER,
	created_at    INTEGER
);

-- local_favorite：本地收藏，仅存展示所需的最小字段。
CREATE TABLE IF NOT EXISTS local_favorite (
	series_id  TEXT PRIMARY KEY,
	title      TEXT,
	cover      TEXT,
	created_at INTEGER
);

-- local_like：本地点赞，目标可以是剧集也可以是单集，故用复合主键。
CREATE TABLE IF NOT EXISTS local_like (
	target_id   TEXT,
	target_type TEXT,
	created_at  INTEGER,
	PRIMARY KEY (target_id, target_type)
);

-- progress_updated：按最近观看时间倒序列出「继续观看」。
CREATE INDEX IF NOT EXISTS progress_updated ON progress (updated_at DESC);
`

// schemaStatements 把 schemaSQL 拆成单条语句，便于逐条执行与定位错误。
//
// DDL 内不含分号，按 ';' 切分是安全的；空段与纯注释段会被跳过。
func schemaStatements() []string {
	var out []string
	cur := make([]byte, 0, 256)
	flush := func() {
		s := trimSpaceBytes(cur)
		cur = cur[:0]
		if len(s) == 0 {
			return
		}
		// 丢掉「只有注释」的片段。
		if isCommentOnly(s) {
			return
		}
		out = append(out, string(s))
	}
	for i := 0; i < len(schemaSQL); i++ {
		c := schemaSQL[i]
		if c == ';' {
			flush()
			continue
		}
		cur = append(cur, c)
	}
	flush()
	return out
}

// trimSpaceBytes 去掉首尾空白（含换行与制表符）。
func trimSpaceBytes(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpaceByte(b[start]) {
		start++
	}
	for end > start && isSpaceByte(b[end-1]) {
		end--
	}
	return b[start:end]
}

// isSpaceByte 报告 b 是否为空白字符。
func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// isCommentOnly 报告片段是否只包含 -- 注释行。
func isCommentOnly(b []byte) bool {
	for _, line := range splitLines(b) {
		line = trimSpaceBytes(line)
		if len(line) == 0 {
			continue
		}
		if !(len(line) >= 2 && line[0] == '-' && line[1] == '-') {
			return false
		}
	}
	return true
}

// splitLines 按换行切分字节串。
func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	out = append(out, b[start:])
	return out
}
