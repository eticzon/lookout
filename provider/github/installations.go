package github

import (
	"context"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation"
	"github.com/google/go-github/github"
	"github.com/src-d/lookout"
	"github.com/src-d/lookout/util/cache"
	vcsurl "gopkg.in/sourcegraph/go-vcsurl.v1"
	log "gopkg.in/src-d/go-log.v1"
)

// Installations keeps github installations and allows to sync them
type Installations struct {
	appID      int
	privateKey string
	appClient  *github.Client

	cache *cache.ValidableCache

	// [installationID]installationClient
	clients map[int64]*Client

	Pool *ClientPool
}

// NewInstallations creates a new Installations using the App ID and private key
func NewInstallations(appID int, privateKey string, cache *cache.ValidableCache) (*Installations, error) {
	// Use App authorization to list installations
	appTr, err := ghinstallation.NewAppsTransportKeyFromFile(
		http.DefaultTransport, appID, privateKey)
	if err != nil {
		return nil, err
	}

	appClient := github.NewClient(&http.Client{Transport: appTr})
	app, _, err := appClient.Apps.Get(context.TODO(), "")
	if err != nil {
		return nil, err
	}

	log.Infof("authorized as GitHub application %q, ID %v", app.GetName(), app.GetID())

	i := &Installations{
		appID:      appID,
		privateKey: privateKey,
		appClient:  appClient,
		cache:      cache,
		clients:    make(map[int64]*Client),
		Pool:       NewClientPool(),
	}

	return i, nil
}

// Sync update state from github
func (t *Installations) Sync() error {
	log.Infof("syncing installations with github")

	installations, _, err := t.appClient.Apps.ListInstallations(context.TODO(), &github.ListOptions{})
	if err != nil {
		return err
	}
	log.Debugf("found %d installations", len(installations))

	new := make(map[int64]*github.Installation, len(installations))
	for _, installation := range installations {
		new[installation.GetID()] = installation
	}

	// remove revoked installations
	for id := range t.clients {
		if _, ok := new[id]; !ok {
			log.Debugf("remove installation %d", id)
			t.removeInstallation(id)
		}
	}

	// add new installations
	for id := range new {
		if _, ok := t.clients[id]; !ok {
			log.Debugf("add installation %d", id)
			t.addInstallation(id)
		}
	}

	// sync repos for all available installations
	for id, c := range t.clients {
		repos, err := t.getRepos(c)
		if err != nil {
			return err
		}
		log.Debugf("%d repositories found for installation %d", len(repos), id)
		t.Pool.Update(c, repos)
	}

	return nil
}

func (t *Installations) addInstallation(id int64) error {
	c, err := t.createClient(id)
	if err != nil {
		return err
	}

	t.clients[id] = c

	return nil
}

func (t *Installations) removeInstallation(id int64) {
	t.Pool.RemoveClient(t.clients[id])

	delete(t.clients, id)
}

func (t *Installations) createClient(installationID int64) (*Client, error) {
	itr, err := ghinstallation.NewKeyFromFile(http.DefaultTransport,
		t.appID, int(installationID), t.privateKey)
	if err != nil {
		return nil, err
	}

	// TODO (carlosms): hardcoded, take from config
	watchMinInterval := ""
	return NewClient(itr, t.cache, watchMinInterval), nil
}

func (t *Installations) getRepos(iClient *Client) ([]*lookout.RepositoryInfo, error) {
	ghRepos, _, err := iClient.Apps.ListRepos(context.TODO(), &github.ListOptions{})
	if err != nil {
		return nil, err
	}

	repos := make([]*lookout.RepositoryInfo, len(ghRepos))
	for i, ghRepo := range ghRepos {
		repo, err := vcsurl.Parse(*ghRepo.HTMLURL)
		if err != nil {
			return nil, err
		}

		repos[i] = repo
	}

	return repos, nil
}
