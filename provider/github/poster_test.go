package github

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/google/go-github/github"
	"github.com/gregjones/httpcache"
	"github.com/src-d/lookout"
	"github.com/src-d/lookout/util/cache"
	"github.com/stretchr/testify/suite"
	"gopkg.in/src-d/go-git.v4/plumbing"
)

var (
	hash1 = "f67e5455a86d0f2a366f1b980489fac77a373bd0"
	hash2 = "02801e1a27a0a906d59530aeb81f4cd137f2c717"
	base1 = plumbing.ReferenceName("base")
	head1 = plumbing.ReferenceName("refs/pull/42/head")
)

var (
	mockEvent = &lookout.ReviewEvent{
		Provider: Provider,
		CommitRevision: lookout.CommitRevision{
			Base: lookout.ReferencePointer{
				InternalRepositoryURL: "https://github.com/foo/bar",
				ReferenceName:         base1,
				Hash:                  hash1,
			},
			Head: lookout.ReferencePointer{
				InternalRepositoryURL: "https://github.com/foo/bar",
				ReferenceName:         head1,
				Hash:                  hash2,
			}}}

	badProviderEvent = &lookout.ReviewEvent{
		Provider: "badprovider",
		CommitRevision: lookout.CommitRevision{
			Base: lookout.ReferencePointer{
				InternalRepositoryURL: "https://github.com/foo/bar",
			}}}

	noRepoEvent = &lookout.ReviewEvent{
		Provider: Provider,
	}

	badReferenceEvent = &lookout.ReviewEvent{
		Provider: Provider,
		CommitRevision: lookout.CommitRevision{
			Base: lookout.ReferencePointer{
				InternalRepositoryURL: "https://github.com/foo/bar",
			},
			Head: lookout.ReferencePointer{
				InternalRepositoryURL: "https://github.com/foo/bar",
				ReferenceName:         plumbing.ReferenceName("BAD"),
			}}}
)

var mockComments = []*lookout.Comment{
	&lookout.Comment{
		Text: "Global comment",
	}, &lookout.Comment{
		File: "main.go",
		Text: "File comment",
	}, &lookout.Comment{
		File: "main.go",
		Line: 5,
		Text: "Line comment",
	}, &lookout.Comment{
		Text: "Another global comment",
	}}

var mockAnalyzerComments = []lookout.AnalyzerComments{
	lookout.AnalyzerComments{
		Config: lookout.AnalyzerConfig{
			Name: "mock",
		},
		Comments: mockComments,
	}}

type PosterTestSuite struct {
	suite.Suite
	mux    *http.ServeMux
	server *httptest.Server
	pool   *ClientPool
}

func (s *PosterTestSuite) SetupTest() {
	s.mux = http.NewServeMux()
	s.server = httptest.NewServer(s.mux)

	cache := cache.NewValidableCache(httpcache.NewMemoryCache())
	githubURL, _ := url.Parse(s.server.URL + "/")

	repoURLs := []string{"github.com/foo/bar"}
	s.pool = newTestPool(s.Suite, repoURLs, githubURL, cache)
}

func (s *PosterTestSuite) TearDownTest() {
	s.server.Close()
}

var mockedPatch = `@@ -3,0 +3,10 @@
+1
+2
+3
+4
+5
+6
+7
+8
+9
+10`

func (s *PosterTestSuite) compareHandle(compareCalled *bool) {
	s.mux.HandleFunc("/repos/foo/bar/compare/"+hash1+"..."+hash2, func(w http.ResponseWriter, r *http.Request) {
		s.False(*compareCalled)
		*compareCalled = true

		cc := &github.CommitsComparison{
			Files: []github.CommitFile{github.CommitFile{
				Filename: strptr("main.go"),
				Patch:    strptr(mockedPatch),
			}}}
		json.NewEncoder(w).Encode(cc)
	})
}

func (s *PosterTestSuite) TestPostOK() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	createReviewsCalled := false
	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		s.False(createReviewsCalled)
		createReviewsCalled = true

		body, err := ioutil.ReadAll(r.Body)
		s.NoError(err)

		expected, _ := json.Marshal(&github.PullRequestReviewRequest{
			CommitID: &mockEvent.Head.Hash,
			Body:     strptr("Global comment\n\nAnother global comment"),
			Event:    strptr(commentEvent),
			Comments: []*github.DraftReviewComment{&github.DraftReviewComment{
				Path:     strptr("main.go"),
				Body:     strptr("File comment"),
				Position: intptr(1),
			}, &github.DraftReviewComment{
				Path:     strptr("main.go"),
				Position: intptr(3),
				Body:     strptr("Line comment"),
			}}})
		s.JSONEq(string(expected), string(body))

		resp := &github.Response{Response: &http.Response{StatusCode: 200}}
		json.NewEncoder(w).Encode(resp)
	})

	p := &Poster{pool: s.pool}
	err := p.Post(context.Background(), mockEvent, mockAnalyzerComments)
	s.NoError(err)

	s.True(createReviewsCalled)
}

func (s *PosterTestSuite) TestPostFooter() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	createReviewsCalled := false
	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		s.False(createReviewsCalled)
		createReviewsCalled = true

		body, err := ioutil.ReadAll(r.Body)
		s.NoError(err)

		expected, _ := json.Marshal(&github.PullRequestReviewRequest{
			CommitID: &mockEvent.Head.Hash,
			Body:     strptr("Global comment\n\nTo post feedback go to https://foo.bar/feedback\n\nAnother global comment\n\nTo post feedback go to https://foo.bar/feedback"),
			Event:    strptr(commentEvent),
			Comments: []*github.DraftReviewComment{&github.DraftReviewComment{
				Path:     strptr("main.go"),
				Body:     strptr("File comment\n\nTo post feedback go to https://foo.bar/feedback"),
				Position: intptr(1),
			}, &github.DraftReviewComment{
				Path:     strptr("main.go"),
				Position: intptr(3),
				Body:     strptr("Line comment\n\nTo post feedback go to https://foo.bar/feedback"),
			}}})
		s.JSONEq(string(expected), string(body))

		resp := &github.Response{Response: &http.Response{StatusCode: 200}}
		json.NewEncoder(w).Encode(resp)
	})

	aComments := mockAnalyzerComments
	aComments[0].Config.Feedback = "https://foo.bar/feedback"

	p := &Poster{
		pool: s.pool,
		conf: ProviderConfig{
			CommentFooter: "To post feedback go to %s",
		},
	}
	err := p.Post(context.Background(), mockEvent, aComments)
	s.NoError(err)

	s.True(createReviewsCalled)
}

func (s *PosterTestSuite) TestPostBadProvider() {
	p := &Poster{pool: s.pool}

	err := p.Post(context.Background(), badProviderEvent, mockAnalyzerComments)
	s.True(ErrEventNotSupported.Is(err))
	s.Equal("event not supported: unsupported provider: badprovider", err.Error())
}

func (s *PosterTestSuite) TestPostBadReferenceNoRepository() {
	p := &Poster{pool: s.pool}

	err := p.Post(context.Background(), noRepoEvent, mockAnalyzerComments)
	s.True(ErrEventNotSupported.Is(err))
	s.Equal("event not supported: nil repository", err.Error())
}

func (s *PosterTestSuite) TestPostBadReference() {
	p := &Poster{pool: s.pool}

	err := p.Post(context.Background(), badReferenceEvent, mockAnalyzerComments)
	s.True(ErrEventNotSupported.Is(err))
	s.Equal("event not supported: bad PR: BAD", err.Error())
}

func (s *PosterTestSuite) TestPostHttpError() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	p := &Poster{pool: s.pool}
	err := p.Post(context.Background(), mockEvent, mockAnalyzerComments)
	s.IsType(ErrGitHubAPI.New(), err)
}

func (s *PosterTestSuite) TestPostHttpTimeout() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	timeout := 10 * time.Millisecond

	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(timeout * 2)
	})

	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()

	p := &Poster{pool: s.pool}
	err := p.Post(ctx, mockEvent, mockAnalyzerComments)
	s.IsType(ErrGitHubAPI.New(), err)
}

func (s *PosterTestSuite) TestPostHttpJSONErr() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Status":"","StatusCode":200,"badjson!!!`))
	})

	p := &Poster{pool: s.pool}
	err := p.Post(context.Background(), mockEvent, mockAnalyzerComments)
	s.IsType(ErrGitHubAPI.New(), err)
}

func (s *PosterTestSuite) TestPostOutOfRange() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	createReviewsCalled := false
	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		s.False(createReviewsCalled)
		createReviewsCalled = true

		body, err := ioutil.ReadAll(r.Body)
		s.NoError(err)

		expected, _ := json.Marshal(&github.PullRequestReviewRequest{
			CommitID: &mockEvent.Head.Hash,
			Body:     strptr(""),
			Event:    strptr(commentEvent),
			Comments: []*github.DraftReviewComment{&github.DraftReviewComment{
				Path:     strptr("main.go"),
				Position: intptr(1),
				Body:     strptr("Line comment"),
			}}})
		s.JSONEq(string(expected), string(body))

		resp := &github.Response{Response: &http.Response{StatusCode: 200}}
		json.NewEncoder(w).Encode(resp)
	})

	outRangeComments := []*lookout.Comment{
		&lookout.Comment{
			File: "main.go",
			Line: 1,
			Text: "out of range comment before",
		},
		&lookout.Comment{
			File: "main.go",
			Line: 3,
			Text: "Line comment",
		},
		&lookout.Comment{
			File: "main.go",
			Line: 205,
			Text: "out of range comment after",
		}}

	outRangeAnalyzerComments := []lookout.AnalyzerComments{
		lookout.AnalyzerComments{
			Config: lookout.AnalyzerConfig{
				Name: "mock",
			},
			Comments: outRangeComments,
		}}

	p := &Poster{pool: s.pool}
	err := p.Post(context.Background(), mockEvent, outRangeAnalyzerComments)
	s.NoError(err)

	s.True(createReviewsCalled)
}

func (s *PosterTestSuite) TestPostAllOutOfRange() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	createReviewsCalled := false
	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		createReviewsCalled = true

		resp := &github.Response{Response: &http.Response{StatusCode: 200}}
		json.NewEncoder(w).Encode(resp)
	})

	outRangeComments := []*lookout.Comment{
		&lookout.Comment{
			File: "main.go",
			Line: 1,
			Text: "out of range comment before",
		},
		&lookout.Comment{
			File: "main.go",
			Line: 205,
			Text: "out of range comment after",
		}}

	outRangeAnalyzerComments := []lookout.AnalyzerComments{
		lookout.AnalyzerComments{
			Config: lookout.AnalyzerConfig{
				Name: "mock",
			},
			Comments: outRangeComments,
		}}

	p := &Poster{pool: s.pool}
	err := p.Post(context.Background(), mockEvent, outRangeAnalyzerComments)
	s.NoError(err)

	s.False(createReviewsCalled)
}

func (s *PosterTestSuite) TestPostOutOfRangeAndBody() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	createReviewsCalled := false
	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		s.False(createReviewsCalled)
		createReviewsCalled = true

		body, err := ioutil.ReadAll(r.Body)
		s.NoError(err)

		expected, _ := json.Marshal(&github.PullRequestReviewRequest{
			CommitID: &mockEvent.Head.Hash,
			Body:     strptr("Body comment"),
			Event:    strptr(commentEvent),
		})
		s.JSONEq(string(expected), string(body))

		resp := &github.Response{Response: &http.Response{StatusCode: 200}}
		json.NewEncoder(w).Encode(resp)
	})

	outRangeComments := []*lookout.Comment{
		&lookout.Comment{
			File: "main.go",
			Line: 1,
			Text: "out of range comment before",
		},
		&lookout.Comment{
			Text: "Body comment",
		},
		&lookout.Comment{
			File: "main.go",
			Line: 205,
			Text: "out of range comment after",
		}}

	outRangeAnalyzerComments := []lookout.AnalyzerComments{
		lookout.AnalyzerComments{
			Config: lookout.AnalyzerConfig{
				Name: "mock",
			},
			Comments: outRangeComments,
		}}

	p := &Poster{pool: s.pool}
	err := p.Post(context.Background(), mockEvent, outRangeAnalyzerComments)
	s.NoError(err)

	s.True(createReviewsCalled)
}

func (s *PosterTestSuite) TestPostOKAndWrongFile() {
	compareCalled := false
	s.compareHandle(&compareCalled)

	createReviewsCalled := false
	s.mux.HandleFunc("/repos/foo/bar/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) {
		s.False(createReviewsCalled)
		createReviewsCalled = true

		body, err := ioutil.ReadAll(r.Body)
		s.NoError(err)

		expected, _ := json.Marshal(&github.PullRequestReviewRequest{
			CommitID: &mockEvent.Head.Hash,
			Body:     strptr("Global comment\n\nAnother global comment"),
			Event:    strptr(commentEvent),
			Comments: []*github.DraftReviewComment{&github.DraftReviewComment{
				Path:     strptr("main.go"),
				Body:     strptr("File comment"),
				Position: intptr(1),
			}, &github.DraftReviewComment{
				Path:     strptr("main.go"),
				Position: intptr(3),
				Body:     strptr("Line comment"),
			}}})
		s.JSONEq(string(expected), string(body))

		resp := &github.Response{Response: &http.Response{StatusCode: 200}}
		json.NewEncoder(w).Encode(resp)
	})

	customMockComments := []*lookout.Comment{
		&lookout.Comment{
			Text: "Global comment",
		}, &lookout.Comment{
			File: "main.go",
			Text: "File comment",
		}, &lookout.Comment{
			File: "main.go",
			Line: 5,
			Text: "Line comment",
		}, &lookout.Comment{
			Text: "Another global comment",
		}, &lookout.Comment{
			File: "file-does-not-exist.txt",
			Line: 5,
			Text: "Line comment",
		}}

	customMockAnalyzerComments := []lookout.AnalyzerComments{
		lookout.AnalyzerComments{
			Config: lookout.AnalyzerConfig{
				Name: "mock",
			},
			Comments: customMockComments,
		}}

	p := &Poster{pool: s.pool}
	err := p.Post(context.Background(), mockEvent, customMockAnalyzerComments)
	s.NoError(err)

	s.True(createReviewsCalled)
}

func (s *PosterTestSuite) TestStatusOK() {
	createStatusCalled := false

	s.mux.HandleFunc("/repos/foo/bar/statuses/02801e1a27a0a906d59530aeb81f4cd137f2c717", func(w http.ResponseWriter, r *http.Request) {
		s.False(createStatusCalled)
		createStatusCalled = true

		body, err := ioutil.ReadAll(r.Body)
		s.NoError(err)

		expected, _ := json.Marshal(&github.RepoStatus{
			State:       strptr("pending"),
			TargetURL:   strptr("https://github.com/src-d/lookout"),
			Description: strptr("The analysis is in progress"),
			Context:     strptr("lookout"),
		})
		s.JSONEq(string(expected), string(body))

		rs := &github.RepoStatus{
			ID:          int64ptr(1234),
			URL:         strptr("https://api.github.com/repos/foo/bar/statuses/1234"),
			State:       strptr("success"),
			TargetURL:   strptr("https://github.com/foo/bar"),
			Description: strptr("description"),
			Context:     strptr("lookout"),
		}
		json.NewEncoder(w).Encode(rs)
	})

	p := &Poster{pool: s.pool}
	err := p.Status(context.Background(), mockEvent, lookout.PendingAnalysisStatus)
	s.NoError(err)

	s.True(createStatusCalled)
}

func (s *PosterTestSuite) TestStatusBadProvider() {
	p := &Poster{pool: s.pool}
	err := p.Status(context.Background(), badProviderEvent, lookout.PendingAnalysisStatus)

	s.True(ErrEventNotSupported.Is(err))
	s.Equal("event not supported: unsupported provider: badprovider", err.Error())
}

func (s *PosterTestSuite) TestStatusBadReferenceNoRepository() {
	p := &Poster{pool: s.pool}
	err := p.Status(context.Background(), noRepoEvent, lookout.PendingAnalysisStatus)
	s.True(ErrEventNotSupported.Is(err))
	s.Equal("event not supported: nil repository", err.Error())
}

func (s *PosterTestSuite) TestStatusBadReference() {
	p := &Poster{pool: s.pool}
	err := p.Status(context.Background(), badReferenceEvent, lookout.PendingAnalysisStatus)
	s.True(ErrEventNotSupported.Is(err))
	s.Equal("event not supported: bad PR: BAD", err.Error())
}

func (s *PosterTestSuite) TestStatusHttpError() {
	s.mux.HandleFunc("/repos/foo/bar/statuses/02801e1a27a0a906d59530aeb81f4cd137f2c717", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	p := &Poster{pool: s.pool}
	err := p.Status(context.Background(), mockEvent, lookout.PendingAnalysisStatus)
	s.IsType(ErrGitHubAPI.New(), err)
}

func (s *PosterTestSuite) TestStatusHttpTimeout() {
	timeout := 10 * time.Millisecond

	s.mux.HandleFunc("/repos/foo/bar/statuses/02801e1a27a0a906d59530aeb81f4cd137f2c717", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(timeout * 2)
	})

	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()

	p := &Poster{pool: s.pool}
	err := p.Status(ctx, mockEvent, lookout.PendingAnalysisStatus)
	s.IsType(ErrGitHubAPI.New(), err)
}

func (s *PosterTestSuite) TestStatusHttpJSONErr() {
	s.mux.HandleFunc("/repos/foo/bar/statuses/02801e1a27a0a906d59530aeb81f4cd137f2c717", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":1234,"url":"https://api.github.com/repos/foo/bar/statuses/1234","state":"success","badjson!!!`))
	})

	p := &Poster{pool: s.pool}
	err := p.Status(context.Background(), mockEvent, lookout.PendingAnalysisStatus)
	s.IsType(ErrGitHubAPI.New(), err)
}

func TestPosterTestSuite(t *testing.T) {
	suite.Run(t, new(PosterTestSuite))
}

func strptr(v string) *string {
	return &v
}

func intptr(v int) *int {
	return &v
}

func int64ptr(v int64) *int64 {
	return &v
}

func TestSplitReview(t *testing.T) {
	require := require.New(t)

	n := 2

	rw := &github.PullRequestReviewRequest{
		Event: strptr(commentEvent),
		Body:  strptr("body"),
	}

	rw.Comments = []*github.DraftReviewComment{
		{Body: strptr("comment1")},
	}

	r := splitReview(rw, n)
	require.Len(r, 1)
	require.Equal([]*github.PullRequestReviewRequest{rw}, r)

	rw.Comments = []*github.DraftReviewComment{
		{Body: strptr("comment1")},
		{Body: strptr("comment2")},
		{Body: strptr("comment3")},
	}

	r = splitReview(rw, n)
	require.Len(r, 2)
	require.Equal([]*github.PullRequestReviewRequest{
		{
			Event: strptr(commentEvent),
			Body:  strptr(""),
			Comments: []*github.DraftReviewComment{
				{Body: strptr("comment1")},
				{Body: strptr("comment2")},
			},
		},
		{
			Event: strptr(commentEvent),
			Body:  strptr("body"),
			Comments: []*github.DraftReviewComment{
				{Body: strptr("comment3")},
			},
		},
	}, r)

	rw.Comments = []*github.DraftReviewComment{
		{Body: strptr("comment1")},
		{Body: strptr("comment2")},
		{Body: strptr("comment3")},
		{Body: strptr("comment4")},
		{Body: strptr("comment5")},
		{Body: strptr("comment6")},
	}

	r = splitReview(rw, n)
	require.Len(r, 3)
}
