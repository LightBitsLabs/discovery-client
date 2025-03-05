// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package clientconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/lightbitslabs/discovery-client/metrics"
	"github.com/lightbitslabs/discovery-client/model"
	"github.com/lightbitslabs/discovery-client/pkg/hostapi"
)

type ReferralKey struct {
	Ip       string
	Port     uint16
	DPSubNqn string // Datapath subsystem nqn. The referral log page entry does not contain it but we need it to create a new connection based on the referral
	Hostnqn  string // The referral log page entry does not contain it but we need it to create a new connection based on the referral
}

type ReferralMap map[ReferralKey]*hostapi.NvmeDiscPageEntry

type TKey struct {
	transport string
	Ip        string
	port      int
	// subsystem nqn
	Nqn     string
	hostnqn string
}

type Connection struct {
	Hostnqn      string
	Hostid       string
	Key          TKey
	Ctx          context.Context
	cancel       context.CancelFunc
	log          *logrus.Entry
	AENChan      chan hostapi.AENStruct
	ConnectionID hostapi.ConnectionID
	State        bool
	CtrlLossTMO  *int // seconds
}

func newConnection(ctx context.Context, key TKey, ctrlLossTMO *int) *Connection {
	c := &Connection{
		Key:         key,
		log:         logrus.WithFields(logrus.Fields{"traddr": key.Ip, "trsvcid": key.port, "nqn": key.Nqn}),
		AENChan:     make(chan hostapi.AENStruct),
		CtrlLossTMO: ctrlLossTMO,
	}
	c.Ctx, c.cancel = context.WithCancel(ctx)
	c.SetState(false)
	return c
}

func (c *Connection) Stop() {
	c.cancel()
	close(c.AENChan)
}

func (c *Connection) GetDiscoveryRequest(kato time.Duration) *hostapi.DiscoverRequest {
	return &hostapi.DiscoverRequest{
		Traddr:    c.Key.Ip,
		Transport: c.Key.transport,
		Trsvcid:   c.Key.port,
		Hostnqn:   c.Hostnqn,
		Kato:      kato,
		AENChan:   c.AENChan,
	}
}

func (c *Connection) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("connection: %s:%d, id: %s, subsystem nqn: %s, hostnqn: %s, hostid: %s",
		c.Key.Ip, c.Key.port, c.ConnectionID, c.Key.Nqn, c.Hostnqn, c.Hostid))
	return sb.String()
}

func (c *Connection) SetState(newState bool) {
	update := newState != c.State
	c.State = newState
	if newState == true {
		metrics.Metrics.ConnectionState.WithLabelValues(c.Key.transport, c.Key.Ip, strconv.Itoa(c.Key.port), c.Key.Nqn).Set(1)
	} else {
		metrics.Metrics.ConnectionState.WithLabelValues(c.Key.transport, c.Key.Ip, strconv.Itoa(c.Key.port), c.Key.Nqn).Set(0)
	}
	if update {
		str := map[bool]string{true: "not-connected ===> connected", false: "connected ===> not-connected"}[newState]
		c.log.Debugf("%s change state: %s", c, str)
	}
}

type ClusterConnections struct {
	ClusterConnectionsMap map[TKey]*Connection
	ActiveConnection      *Connection
}

func (c ClusterConnections) GetRandomConnectionList() []*Connection {
	clusterConnectionsList := make([]*Connection, len(c.ClusterConnectionsMap))
	ind := 0
	for _, conn := range c.ClusterConnectionsMap {
		clusterConnectionsList[ind] = conn
		ind++
	}
	//Generate a random permutation of connections order to balance used target among clients
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(clusterConnectionsList), func(i, j int) {
		clusterConnectionsList[i], clusterConnectionsList[j] = clusterConnectionsList[j], clusterConnectionsList[i]
	})
	return clusterConnectionsList
}

type ClientClusterPair struct {
	ClusterNqn string
	HostNqn    string
}

func (pair ClientClusterPair) isEmpty() bool {
	return pair.ClusterNqn == "" && pair.HostNqn == ""
}

type ConnectionMap map[ClientClusterPair]ClusterConnections

func (cm ConnectionMap) AddConnection(key TKey, conn *Connection) {
	pair := ClientClusterPair{
		ClusterNqn: key.Nqn,
		HostNqn:    key.hostnqn,
	}
	if _, ok := cm[pair]; !ok {
		clusterConns := ClusterConnections{
			ClusterConnectionsMap: make(map[TKey]*Connection),
		}
		cm[pair] = clusterConns
	}
	cm[pair].ClusterConnectionsMap[key] = conn
}

func (cm ConnectionMap) DeleteConnection(clientClusterPair ClientClusterPair, key TKey) {
	delete(cm[clientClusterPair].ClusterConnectionsMap, key)
}

func (cc ClusterConnections) Exists(c *Connection) bool {
	for _, conn := range cc.ClusterConnectionsMap {
		if reflect.DeepEqual(conn, c) {
			return true
		}
	}
	return false
}

type Cache interface {
	// Run start watching for changes
	Run(sync bool) error
	// Stop stop watching for changes
	Stop()
	// Clear clears the entries we have till now
	Clear()
	Connections() <-chan ConnectionMap
	HandleReferrals(referrals ReferralMap) error
}

type cache struct {
	userDirPath       string
	cacheEntries      []*Entry
	clearCh           chan bool
	ctx               context.Context
	cancel            context.CancelFunc
	log               *logrus.Entry
	connections       ConnectionMap
	connectionsChan   chan ConnectionMap
	internalDirPath   string
	autoDetectEntries *model.AutoDetectEntries
}

// NewCache return a Cache implementation.
func NewCache(ctx context.Context, userDirPath, internalDirPath string, autoDetectEntries *model.AutoDetectEntries) Cache {
	c := &cache{
		userDirPath:       userDirPath,
		log:               logrus.WithFields(logrus.Fields{}),
		cacheEntries:      []*Entry{},
		connections:       ConnectionMap{},
		connectionsChan:   make(chan ConnectionMap),
		internalDirPath:   internalDirPath,
		autoDetectEntries: autoDetectEntries,
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	return c
}

func (c *cache) Connections() <-chan ConnectionMap {
	return c.connectionsChan
}

func (c *cache) createReferralsFile() error {
	entries := []Entry{}
	for _, entry := range c.cacheEntries {
		entries = append(entries, *entry)
	}
	refs := referrals{CreationTime: time.Now(), Entries: entries}
	content, err := json.MarshalIndent(refs, "", "\t")
	if err != nil {
		c.log.WithError(err).Error("Failed to create internal json")
		return err
	}

	tmpfile, err := os.CreateTemp(c.internalDirPath, "tmp_internal.json")
	if err != nil {
		c.log.WithError(err).Errorf("Failed to create temp file")
		return err
	}
	defer os.Remove(tmpfile.Name())
	if _, err = tmpfile.Write(content); err != nil {
		c.log.WithError(err).Errorf("Failed to write referrals to temp file")
		return err
	}
	if err := tmpfile.Sync(); err != nil {
		c.log.WithError(err).Errorf("Failed to sync temp file")
		return err
	}
	if err = tmpfile.Chmod(0644); err != nil {
		c.log.WithError(err).Errorf("Failed to chmod temp file to 0644")
		return err
	}
	if err = tmpfile.Close(); err != nil {
		c.log.WithError(err).Errorf("Failed to close temp file")
		return err
	}
	filePath := path.Join(c.internalDirPath, InternalJson)
	err = os.Rename(tmpfile.Name(), filePath)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to rename temp file %s to referral file %s", tmpfile.Name(), filePath)
	}
	return err
}

func (c *cache) useInternalJson() (use bool, entries []Entry, err error) {
	// A function for determining at startup if internal json or user folder should be used to get initial entries and connections
	existingEntries := []Entry{}
	jsonPath := path.Join(c.internalDirPath, InternalJson)
	info, err := os.Stat(jsonPath)
	if os.IsNotExist(err) {
		return false, existingEntries, nil
	}
	if info.IsDir() {
		c.log.Errorf("%s is a directory, expected a json file", jsonPath)
		return false, existingEntries, nil
	}
	content, _ := os.ReadFile(jsonPath)
	if len(content) == 0 {
		c.log.Errorf("%s is unexpectedly an empty file", jsonPath)
		return false, existingEntries, nil
	}
	var oldRefs referrals
	err = json.Unmarshal(content, &oldRefs)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to unpack %s", jsonPath)
		return false, existingEntries, err
	}
	existingEntries = oldRefs.Entries
	if len(existingEntries) == 0 {
		return false, existingEntries, nil
	}
	referralJsonCreationTime := oldRefs.CreationTime
	c.log.Debugf("oldEntries = %v, oldTime = %v", existingEntries, referralJsonCreationTime)
	lastUserUpdateTime, err := lastUpdate(c.userDirPath)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to get last update time of directory %s", c.userDirPath)
		return false, nil, err
	}
	if lastUserUpdateTime.After(referralJsonCreationTime) {
		c.log.Debugf("User directory %s updated after internal directory %s. Ignoring internal json", c.userDirPath, c.internalDirPath)
		return false, existingEntries, nil
	}
	return true, existingEntries, nil
}

func (c *cache) sync() error {
	/*	This function is called at cache start and is responsible for creating initial connections and Entries
		There are two cases to consider:
		1. Our json file was last updated after the user folder. In this case we rely on our internal json which may contain
		entries obtained through referrals. No need to update the referrals file in this case.
		2. Last change in the user folder (through which the user may add files with entries) is newer than our internal json.
		In this case we disregard our internal json entries and rely on the user. After that we update the internal referrals file.	*/

	useJson, jsonEntries, err := c.useInternalJson()
	if err != nil {
		return err
	}
	changedPairs := []ClientClusterPair{} // a list of pairs with new connections
	if useJson {
		for _, e := range jsonEntries {
			var entry = e
			if err := entry.verify(); err != nil {
				c.log.Errorf("Failed to form entry from json %+v", entry)
				return err
			}
			pair, _ := c.addEntry(&entry)
			if !pair.isEmpty() {
				changedPairs = append(changedPairs, pair)
			}
		}
	} else {
		userFiles, err := os.ReadDir(c.userDirPath)
		if err != nil {
			return err
		}
		for _, file := range userFiles {
			if file.IsDir() {
				continue
			}
			c.log.Debugf("Running sync with file %s", file.Name())
			changedPairsFromFile, _ := c.fileAdded(filepath.Join(c.userDirPath, file.Name()))
			changedPairs = append(changedPairs, changedPairsFromFile...)
		}
		c.createReferralsFile()
	}
	if len(changedPairs) > 0 {
		go func() {
			c.notifyChange(changedPairs)
		}()
	}
	return nil
}

// Run watch the desired folder
// sync state that we want to look at the already existing files before watching
// future events.
func (c *cache) Run(sync bool) error {
	// Handle issue: https://lightbitslabs.atlassian.net/browse/LBM1-18864
	if c.autoDetectEntries != nil &&
		c.autoDetectEntries.Enabled &&
		ShouldGenerateAutoDetectedEntries(c.userDirPath, c.internalDirPath) {
		allEntries, err := DetectEntriesByIOControllers(NvmeCtrlPath, uint(c.autoDetectEntries.DiscoveryServicePort))
		if err != nil {
			c.log.WithField("error", err).Fatal("failed to detect entries from IO Controllers")
			return err
		}
		filepath := path.Join(c.userDirPath, c.autoDetectEntries.Filename)
		StoreEntries(filepath, allEntries)
		if err != nil {
			c.log.WithField("error", err).Fatal("failed to store entries")
			return err
		}
	}

	// there is a potential race here if we sync and during the process a file is added/removed.
	// we might miss a file cause we don't yet listen on file notifications.
	// this is a rare scenario and the consumer is to make sure that it does not happen.
	// we can solve this issue but the complexity and time it takes is not worth it.
	if sync {
		if err := c.sync(); err != nil {
			return err
		}
	}

	var fw FileWatcher
	ch, err := fw.Watch(c.ctx, c.userDirPath)
	if err != nil {
		return err
	}
	go func() {
		for {
			select {
			case event := <-ch:
				// we ignore all files starting with this prefix
				filename := path.Base(event.Name)
				if strings.HasPrefix(filename, model.DiscoveryClientReservedPrefix) {
					continue
				}
				switch event.Op {
				case Create, Rename:
					pairs, _ := c.fileAdded(event.Name)
					c.createReferralsFile()
					if len(pairs) > 0 {
						c.notifyChange(pairs)
					}
				case Remove:
					// today we don't handle remove events knowingly,
					// but we want to prevent the warn msg to log.
					continue
				default:
					c.log.Warnf("unhandled event for file: %q. op: %s", event.Name, event.Op)
				}
			case <-c.clearCh:
				c.cacheEntries = nil
			case <-c.ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (c *cache) notifyChange(changedClientClusterPairs []ClientClusterPair) {
	//Alerts the service on clusters that changed
	c.log.Debugf("Notifying change with pairs: %+v", changedClientClusterPairs)
	changedPairs := make(ConnectionMap)
	for _, pair := range changedClientClusterPairs {
		if pair.isEmpty() {
			c.log.Warn("Got an empty client cluster pair with changed connections")
			continue
		}
		clusterConnections, ok := c.connections[pair]
		if !ok {
			c.log.Warnf("Attempted to notify on change at pair %+v but no connections on this pair were found", pair)
			continue
		}
		changedPairs[pair] = clusterConnections
	}
	if len(changedPairs) > 0 {
		c.connectionsChan <- changedPairs
	}
}

func (c *cache) Stop() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

func (c *cache) Clear() {
	c.clearCh <- true
}

func (c *cache) fileAdded(filename string) ([]ClientClusterPair, error) {
	// called if a user file was added or at startup when internal json entries file is outdated
	// adds file entries to cache entries
	// adds connection if a new connection is required
	// returns a list of pairs of connection subsystemNqn and hostNqn if new connections were added
	c.log.Debugf("Dealing with added file %s", filename)
	pairsSet := map[ClientClusterPair]bool{}
	newEntries, err := parse(filename)
	if err != nil {
		c.log.WithError(err).Errorf("parse file %s failed", filename)
		return nil, err
	}
	c.log.Debugf("Found %d entries in user file %s", len(newEntries), filename)
	for _, newEntry := range newEntries {
		newEntry.Persistent = true
		pair, err := c.addEntry(newEntry)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to deal with user file %s", filename)
			return nil, err
		}
		if !pair.isEmpty() {
			pairsSet[pair] = true
		}
	}
	pairs := []ClientClusterPair{}
	for pair := range pairsSet {
		pairs = append(pairs, pair)
	}
	c.log.Debugf("Returning %+v pairs from file %s", pairs, filename)
	return pairs, nil
}

func (c *cache) existEntry(newEntry *Entry, entriesList []*Entry) bool {
	for _, inListEntry := range entriesList {
		c.log.Debugf("[existEntry] new: %s, existing entry: %s",
			newEntry.String(),
			EntriesToString(entriesList))
		if newEntry.compare(inListEntry) {
			// TODO: check if we need to update the entry, and find a way to
			// propagate the change to the cache connections.
			// (for example, if the ctrlLossTMO changed)
			return true
		}
	}
	return false
}

func (c *cache) addEntry(newEntry *Entry) (ClientClusterPair, error) {
	if c.existEntry(newEntry, c.cacheEntries) {
		c.log.Debugf("entry %+v already found in cache - no need to add", newEntry)
		return ClientClusterPair{}, nil
	}
	if newEntry.EntrySource == EntrySourceReferral && newEntry.CtrlLossTMO == nil {
		for _, entry := range c.cacheEntries {
			if entry.EntrySource == EntrySourceUser && entry.CtrlLossTMO != nil {
				newEntry.CtrlLossTMO = entry.CtrlLossTMO
				break
			}
		}
	}
	c.cacheEntries = append(c.cacheEntries, newEntry)
	c.log.Infof("added cache (len=%d) entry: %s", len(c.cacheEntries), newEntry.String())
	metrics.Metrics.EntriesTotal.WithLabelValues().Inc()

	key := TKey{transport: newEntry.Transport, Ip: newEntry.Traddr,
		port: newEntry.Trsvcid, Nqn: newEntry.Subsysnqn,
		hostnqn: newEntry.Hostnqn}
	pair := ClientClusterPair{
		ClusterNqn: newEntry.Subsysnqn,
		HostNqn:    newEntry.Hostnqn,
	}
	conn, ok := c.connections[pair].ClusterConnectionsMap[key]
	if !ok {
		conn = newConnection(c.ctx, key, newEntry.CtrlLossTMO)
		conn.Hostnqn = newEntry.Hostnqn
		conn.Hostid = newEntry.Hostid
		c.connections.AddConnection(key, conn)
		metrics.Metrics.Connections.WithLabelValues(key.transport, key.Ip, strconv.Itoa(key.port), key.Nqn, conn.Hostnqn).Inc()
		c.log.Debugf("Added %s to cache connections", conn)
		return pair, nil
	}
	err := fmt.Errorf("Entry %+v not cached, though '%s' is in cache", newEntry, conn)
	c.log.WithError(err).Error("Mismatch between cache entries and cache connections")
	return ClientClusterPair{}, err
}

func (c *cache) HandleReferrals(referrals ReferralMap) error {
	if len(referrals) == 0 {
		err := fmt.Errorf("Handle referrals got empty referrals map. This should never happen")
		c.log.WithError(err)
		return err
	}
	c.log.Debugf("Handling %d referrals:", len(referrals))
	for key := range referrals {
		c.log.Debugf("%s:%d", key.Ip, key.Port)
	}
	newConnectionsPairs, err := c.addConnectionsFromReferrals(referrals)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to add new connections from referrals")
	}
	removedConnectionsPairs, err := c.removeConnectionsNotInReferrals(referrals)
	if err != nil {
		c.log.WithError(err).Errorf("Failed to remove connections following referrals")
	}

	changedClientClusterPairs := append(newConnectionsPairs, removedConnectionsPairs...)
	if len(changedClientClusterPairs) > 0 {
		c.log.Debugf("Changes in connection map due to referrals update. Updating internal json and notifying service")
		c.createReferralsFile()
		go func() {
			c.notifyChange(changedClientClusterPairs)
		}()
	}
	return err
}

func (c *cache) addConnectionsFromReferrals(referrals ReferralMap) (newConnectionsPairs []ClientClusterPair, err error) {
	pairs := []ClientClusterPair{}
	if len(referrals) == 0 {
		return pairs, nil
	}
	c.log.Debugf("Checking if new entries are required from referrals")
	for refKey, referral := range referrals {
		newEntry := getEntryFromReferral(refKey, referral)
		pair, err := c.addEntry(newEntry)
		if err != nil {
			c.log.WithError(err).Errorf("Failed to add entry %v", newEntry)
			continue
		}
		if !pair.isEmpty() {
			pairs = append(pairs, pair)
		}
	}

	c.log.Debugf("%d new connections added from referrals", len(pairs))
	return pairs, nil
}

func (c *cache) removeConnectionsNotInReferrals(referrals ReferralMap) (removedConnectionsPairs []ClientClusterPair, err error) {
	pairs := []ClientClusterPair{}
	c.log.Debugf("Checking if entries removal is needed due to referrals")
	referralEntries := []*Entry{}
	var prevPair, currentPair ClientClusterPair
	for refKey, referral := range referrals {
		referralEntries = append(referralEntries, getEntryFromReferral(refKey, referral))
		currentPair = ClientClusterPair{
			ClusterNqn: refKey.DPSubNqn,
			HostNqn:    refKey.Hostnqn,
		}
		// All referrals were obtained from one discovery command with one connection. We expect all to have the same hostnqn and subsysnqn
		if !prevPair.isEmpty() {
			if !reflect.DeepEqual(prevPair, currentPair) {
				return pairs, fmt.Errorf("found different client cluster pair in referrals: %+v and %+v", prevPair, currentPair)
			}
		}
		prevPair = currentPair
	}
	entriesToRemove := []*Entry{}
	for _, cachedEntry := range c.cacheEntries {
		// We remove entries that are not in referrals if they share the same hostnqn and subsystemnqn as the referrals
		// We use the last referral for hostnqn and subsystemnqn after we checked they are equal in all referrals
		if !c.existEntry(cachedEntry, referralEntries) && cachedEntry.Hostnqn == currentPair.HostNqn && cachedEntry.Subsysnqn == currentPair.ClusterNqn {
			c.log.Debugf("Cached entry %+v not found in referrals. Will be removed from cache", cachedEntry)
			entriesToRemove = append(entriesToRemove, cachedEntry)
		}
	}
	if len(entriesToRemove) == 0 {
		c.log.Debug("No entries removal is required due to referrals")
		return pairs, nil
	}
	c.log.Debugf("Going to remove %d entries due to referrals", len(entriesToRemove))
	for _, cachedEntryToRemove := range entriesToRemove {
		if pair, _ := c.deleteEntry(cachedEntryToRemove); !pair.isEmpty() {
			pairs = append(pairs, pair)
		}
	}
	return pairs, nil
}

func getEntryFromReferral(refKey ReferralKey, referral *hostapi.NvmeDiscPageEntry) *Entry {
	return &Entry{
		Transport:   "tcp",
		Trsvcid:     int(referral.TrsvcID),
		Traddr:      referral.Traddr,
		Hostnqn:     refKey.Hostnqn,
		Subsysnqn:   refKey.DPSubNqn,
		Persistent:  true,
		EntrySource: EntrySourceReferral,
		CtrlLossTMO: nil,
	}
}

func (c *cache) deleteEntry(entry *Entry) (connectionPair ClientClusterPair, err error) {
	pair := ClientClusterPair{}
	found := false
	for i, cachedEntry := range c.cacheEntries {
		// compare the pointers between 2 lists in order to find the correct entry to delete.
		if cachedEntry == entry {
			found = true
			c.cacheEntries = append(c.cacheEntries[:i], c.cacheEntries[i+1:]...)
			metrics.Metrics.EntriesTotal.WithLabelValues().Dec()
			c.log.Debugf("Deleted entry %+v from cache", cachedEntry)
			break
		}
	}
	if !found {
		err := fmt.Errorf("Entry to remove was not found in cache")
		c.log.WithError(err).Errorf("Failed to remove entry %+v", entry)
		return pair, err
	}
	pair.ClusterNqn = entry.Subsysnqn
	pair.HostNqn = entry.Hostnqn
	key := TKey{transport: entry.Transport, Ip: entry.Traddr, port: entry.Trsvcid, Nqn: entry.Subsysnqn, hostnqn: entry.Hostnqn}
	conn, ok := c.connections[pair].ClusterConnectionsMap[key]
	if ok {
		c.log.Debugf("Deleting %s from cache connections", conn)
		delete(c.connections[pair].ClusterConnectionsMap, key)
	} else {
		c.log.Warnf("Failed to find a cache connection corresponding to deleted entry")
	}
	return pair, nil
}

func (c *cache) matchReferralEntry(referral *hostapi.NvmeDiscPageEntry, subsystemNqn, hostnqn string, entry *Entry) bool {
	return entry.Trsvcid == int(referral.TrsvcID) &&
		entry.Subsysnqn == subsystemNqn &&
		entry.Traddr != referral.Traddr &&
		entry.Hostnqn == hostnqn
}
