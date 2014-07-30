package main

import (
	"bytes"
	"database/sql"
	"github.com/lib/pq/hstore"
	. "github.com/wikiocracy/cryptoballot/cryptoballot"
	"io/ioutil"
	"net/http"
	"strings"
)

func electionHandler(w http.ResponseWriter, r *http.Request) {
	// Parse URL and route
	urlparts := strings.Split(r.RequestURI, "/")

	// Check for the correct number of request parts
	if len(urlparts) != 3 {
		http.Error(w, "Invalid URL. 404 Not Found.", http.StatusNotFound)
		return
	}

	// Get the electionID
	electionID := urlparts[2]
	if len(electionID) > MaxElectionIDSize || !ValidElectionID.MatchString(electionID) {
		http.Error(w, "Invalid Election ID. 404 Not Found.", http.StatusNotFound)
		return
	}

	switch r.Method {
	case "GET":
		handleGETElection(w, r, electionID)
	case "PUT":
		handlePUTElection(w, r, electionID)
	case "HEAD":
		//@@TODO: handleHEADElection(w, r, electionID)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handlePUTElection(w http.ResponseWriter, r *http.Request, electionID string) {
	err := verifySignatureHeaders(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	election, err := NewElection(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if election.ElectionID != electionID {
		http.Error(w, "Election ID mismatch between body and URL", http.StatusBadRequest)
		return
	}
	if election.PublicKey.String() != r.Header.Get("X-Public-Key") {
		http.Error(w, "Public Key mismatch between headers and body", http.StatusBadRequest)
		return
	}

	// Verify the signature on the election
	err = election.VerifySignature()
	if err != nil {
		http.Error(w, "Error verifying election signature. "+err.Error(), http.StatusBadRequest)
		return
	}

	// Check to make sure this admin exists and has permission to administer elections
	admin := admins.GetUser(election.PublicKey)
	if admin == nil {
		http.Error(w, "Could not find admin with the provided public key of "+election.PublicKey.String(), http.StatusForbidden)
		return
	}
	if !bytes.Equal(admin.PublicKey.Bytes(), election.PublicKey.Bytes()) {
		http.Error(w, "Public Key provided by the election does not match the admin's public key.", http.StatusForbidden)
		return
	}
	if !admin.HasPerm("election-admin") {
		http.Error(w, "This user does not have the `election-admin` permission", http.StatusForbidden)
		return
	}

	// All checks pass. Save the election
	err = saveElectionToDB(election)
	if err != nil {
		http.Error(w, "Error saving election: "+err.Error(), http.StatusInternalServerError)
	}
}

func saveElectionToDB(election *Election) error {
	// Frist transform the tagset into an hstore
	var tags hstore.Hstore
	tags.Map = make(map[string]sql.NullString, len(election.TagSet))
	for key, value := range election.TagSet.Map() {
		tags.Map[key] = sql.NullString{value, true}
	}

	_, err := db.Exec("INSERT INTO elections (election_id, election, startdate, enddate, tags) VALUES ($1, $2, $3, $4, $5)", election.ElectionID, election.String(), election.Start, election.End, tags)
	if err != nil {
		return err
	}

	// Create the election table for storing ballots
	_, err = db.Exec(strings.Replace(ballotsQuery, "<election-id>", election.ElectionID, -1))
	if err != nil {
		return err
	}

	return nil
}

func handleGETElection(w http.ResponseWriter, r *http.Request, electionID string) {
	var rawElection []byte
	err := db.QueryRow("SELECT election FROM elections WHERE election_id = $1", electionID).Scan(&rawElection)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Could not find election with ID "+electionID, http.StatusNotFound)
		} else {
			http.Error(w, "Error reading election from database: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}
	w.Write(rawElection)
	return
}