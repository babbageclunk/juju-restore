// Copyright 2020 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package db

import (
	"crypto/tls"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/juju/collections/set"
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"github.com/juju/replicaset"
	"github.com/juju/version"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/juju-restore/core"
)

var logger = loggo.GetLogger("juju-restore.db")

// DialInfo holds information needed to connect to the database.
type DialInfo struct {
	Hostname string
	Port     string
	Username string
	Password string
	SSL      bool
}

// Dial creates a new connection to the specified database.
func Dial(args DialInfo) (core.Database, error) {
	info := mgo.DialInfo{
		Addrs:    []string{net.JoinHostPort(args.Hostname, args.Port)},
		Database: "admin",
		Username: args.Username,
		Password: args.Password,
		Direct:   true,
	}
	if args.SSL {
		info.DialServer = dialSSL
	}
	session, err := mgo.DialWithInfo(&info)
	if err != nil {
		return nil, errors.Trace(err)
	}
	// We need to set preference to nearest since we're connecting
	// directly, not to all the nodes in the replicaset.
	session.SetMode(readPreferenceNearest, false)
	return &database{session: session, info: args}, nil
}

const readPreferenceNearest = 6

type database struct {
	info    DialInfo
	session *mgo.Session
}

// ReplicaSet is part of core.Database.
func (db *database) ReplicaSet() (core.ReplicaSet, error) {
	status, err := replicaset.CurrentStatus(db.session)
	if err != nil {
		return core.ReplicaSet{}, errors.Trace(err)
	}
	// Current members collection of replicaset contains additional
	// information for the nodes, including machine IDs.
	members, err := replicaset.CurrentMembers(db.session)
	if err != nil {
		return core.ReplicaSet{}, errors.Trace(err)
	}
	mapped := map[int]replicaset.Member{}
	for _, v := range members {
		mapped[v.Id] = v
	}
	machineID := func(member replicaset.Member) string {
		t, k := member.Tags["juju-machine-id"]
		if !k {
			return ""
		}
		return t
	}

	result := core.ReplicaSet{
		Name:    status.Name,
		Members: make([]core.ReplicaSetMember, len(status.Members)),
	}
	for i, m := range status.Members {
		result.Members[i] = core.ReplicaSetMember{
			ID:            m.Id,
			Name:          m.Address,
			Self:          m.Self,
			Healthy:       m.Healthy,
			State:         m.State.String(),
			JujuMachineID: machineID(mapped[m.Id]),
		}
	}
	return result, nil

}

const jobManageModel = 2
const alive = 0

// ControllerInfo is part of core.Database.
func (db *database) ControllerInfo() (core.ControllerInfo, error) {
	var result core.ControllerInfo

	jujuDB := db.session.DB("juju")
	var modelDoc struct {
		ID string `bson:"_id"`
	}
	err := jujuDB.C("models").Find(bson.M{"name": "controller"}).One(&modelDoc)
	if err != nil {
		return core.ControllerInfo{}, errors.Annotate(err, "getting controller model")
	}
	result.ControllerModelUUID = modelDoc.ID

	var settingsDoc struct {
		Settings map[string]interface{} `bson:"settings"`
	}
	modelSettingsKey := modelDoc.ID + ":e"
	err = jujuDB.C("settings").FindId(modelSettingsKey).One(&settingsDoc)
	if err != nil {
		return core.ControllerInfo{}, errors.Annotate(err, "getting controller settings")
	}
	versionVal, ok := settingsDoc.Settings["agent-version"]
	if !ok {
		return core.ControllerInfo{}, errors.Errorf("no agent-version in controller settings")
	}
	versionStr, ok := versionVal.(string)
	if !ok {
		return core.ControllerInfo{}, errors.Errorf("expected agent-version to be a string, got %#v", versionVal)
	}
	result.JujuVersion, err = version.Parse(versionStr)
	if err != nil {
		return core.ControllerInfo{}, errors.Trace(err)
	}

	var machineDoc struct {
		Series string `bson:"series"`
	}
	query := bson.M{
		"model-uuid": modelDoc.ID,
		"jobs":       bson.M{"$in": []int{jobManageModel}},
		"life":       alive,
	}
	iter := jujuDB.C("machines").Find(query).Iter()
	allSeries := set.NewStrings()
	for iter.Next(&machineDoc) {
		result.HANodes++
		allSeries.Add(machineDoc.Series)
	}
	if err := iter.Close(); err != nil {
		return core.ControllerInfo{}, errors.Annotate(err, "getting controller series")
	}

	allSeriesNames := allSeries.SortedValues()
	if len(allSeriesNames) != 1 {
		return core.ControllerInfo{}, errors.Errorf("expected one series, got %#v", allSeriesNames)
	}

	result.Series = allSeriesNames[0]
	return result, nil
}

const restoreBinary = "mongorestore"

func (db *database) buildRestoreArgs(dumpPath string, includeStatusHistory bool) []string {
	args := []string{
		"-vvvvv",
		"--drop",
		"--writeConcern=majority",
		"--host", db.info.Hostname,
		"--port", db.info.Port,
		"--authenticationDatabase=admin",
		"--username", db.info.Username,
		"--password", db.info.Password,
		"--ssl",
		"--sslAllowInvalidCertificates",
		"--stopOnError",
		"--maintainInsertionOrder",
		"--nsExclude=logs.*",
	}
	if !includeStatusHistory {
		args = append(args, "--nsExclude=juju.statuseshistory")
	}
	return append(args, dumpPath)
}

// RestoreFromDump uses mongorestore to load the dump from a backup.
func (db *database) RestoreFromDump(dumpDir, logFile string, includeStatusHistory bool) error {
	command := exec.Command(
		restoreBinary,
		db.buildRestoreArgs(dumpDir, includeStatusHistory)...,
	)
	logger.Debugf("running restore command: %s %s", command.Path, strings.Join(command.Args, " "))
	dest, err := os.Create(logFile)
	if err != nil {
		return errors.Annotatef(err, "opening logfile %q", logFile)
	}
	defer dest.Close()
	command.Stdout = dest
	command.Stderr = dest
	return errors.Annotatef(command.Run(), "running %s", restoreBinary)
}

// Close is part of core.Database.
func (db *database) Close() {
	db.session.Close()
}

func dialSSL(addr *mgo.ServerAddr) (net.Conn, error) {
	c, err := net.Dial("tcp", addr.String())
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
	}
	cc := tls.Client(c, tlsConfig)
	if err := cc.Handshake(); err != nil {
		return nil, err
	}
	return cc, nil
}
