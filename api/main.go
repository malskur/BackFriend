package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"sync"

	_ "github.com/lib/pq"
)

type jsonPlayer struct {
	PlayerID string `json:"playerId"`
	Balance  int    `json:"balance"`
}

type jsonWinner struct {
	PlayerID string `json:"playerId"`
	Prize    int    `json:"prize"`
}

type jsonResult struct {
	TourID  string       `json:"tournamentId"`
	Winners []jsonWinner `json:"winners"`
}

type winner struct {
	PlayerID string `json:"playerId"`
	Balance  int    `json:"balance"`
}

type players struct {
	playerID string
	balance  int
}

type joinings struct {
	tourID         string
	playerID       string
	contribute     int
	contributeToID string
}

type tournaments struct {
	tourID   string
	deposit  int
	playerID string
}

var dbGames *sql.DB
var muxPlayers sync.Mutex
var muxJoinings sync.Mutex
var muxTours sync.Mutex

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "test"
	dbname   = "Games"
)

func init() {
	var err error

	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	dbGames, err = sql.Open("postgres", psqlInfo)

	if err != nil {
		log.Fatal(err)
	}

	if err = dbGames.Ping(); err != nil {
		log.Fatal(err)
	}
}

func main() {
	http.HandleFunc("/take", takePoints)
	http.HandleFunc("/fund", fundPoints)
	http.HandleFunc("/announceTournament", announceTournament)
	http.HandleFunc("/joinTournament", joinTournament)
	http.HandleFunc("/resultTournament", resultTournament)
	http.HandleFunc("/balance", balance)
	http.HandleFunc("/reset", reset)
	http.ListenAndServe(":3000", nil)
}

//
// #1 Take player account
// /take?playerId=P1&points=300   takes 300 points from player P1 account
func takePoints(res http.ResponseWriter, req *http.Request) {
	player := req.FormValue("playerId")
	points, err := strconv.Atoi(req.FormValue("points"))
	if err != nil {
		log.Println("takePoints: " + err.Error())
		http.Error(res, http.StatusText(400), 400)
		return
	}

	muxPlayers.Lock()
	defer muxPlayers.Unlock()

	status := decresePlayerBalance(player, points)
	if status != 0 {
		log.Println("takePoints->decresePlayerBalance: error")
		http.Error(res, http.StatusText(status), status)
		return
	}
}

//
// #1 Fund player account
// /fund?playerId=P1&points=300   funds player P1 with 300 points. If no player exist should create new player
func fundPoints(res http.ResponseWriter, req *http.Request) {
	player := req.FormValue("playerId")
	points, err := strconv.Atoi(req.FormValue("points"))

	if err != nil {
		log.Println("fundPoints: " + err.Error())
		http.Error(res, http.StatusText(400), 400)
		return
	}

	muxPlayers.Lock()
	defer muxPlayers.Unlock()

	status := updatePlayerBalance(player, points)
	if status != 0 {
		log.Println("fundPoints->updatePlayerBalance: error")
		http.Error(res, http.StatusText(status), status)
		return
	}
}

//
// #2 Announce tournament specifying the entry deposit
// /announceTournament?tournamentId=1&deposit=1000
func announceTournament(res http.ResponseWriter, req *http.Request) {
	tour := req.FormValue("tournamentId")

	muxJoinings.Lock()
	defer muxJoinings.Unlock()
	muxTours.Lock()
	defer muxTours.Unlock()

	err := checkTournament(tour)
	if err == nil {
		log.Println("announceTournament->checkTournament: tourID already exist")
		http.Error(res, http.StatusText(400), 400)
		return
	}

	deposit, err := strconv.Atoi(req.FormValue("deposit"))
	if err != nil {
		log.Println("announceTournament: " + err.Error())
		http.Error(res, http.StatusText(400), 400)
		return
	}
	if deposit < 0 {
		log.Println("announceTournament: negative deposit value")
		http.Error(res, http.StatusText(400), 400)
		return
	}

	err = addTournament(tour, deposit)
	if err != nil {
		log.Println("announceTournament->addTournament: " + err.Error())
		http.Error(res, http.StatusText(500), 500)
		return
	}
}

//
// #3 Join player into a tournament and is he backed by a set of backers
// /joinTournament?tournamentId=1&playerId=P1&backerId=P2&backerId=P3
func joinTournament(res http.ResponseWriter, req *http.Request) {
	tour := req.FormValue("tournamentId")
	player := req.FormValue("playerId")

	muxPlayers.Lock()
	defer muxPlayers.Unlock()
	muxJoinings.Lock()
	defer muxJoinings.Unlock()
	muxTours.Lock()
	defer muxTours.Unlock()

	if tour == "" || player == "" {
		log.Println("joinTournament: empty tournamentId/playerId request")
		http.Error(res, http.StatusText(400), 400)
		return
	}

	partners := make([]string, 0)
	partners = append(partners, player)
	backers := req.URL.Query()["backerId"]

	if backers != nil {
		partners = append(partners, backers...)
	}

	//check whether tournament still opened
	err := getTourStatus(tour)
	if err != nil && err != sql.ErrNoRows {
		log.Println("joinTournament->getTourStatus: " + err.Error())
		http.Error(res, http.StatusText(400), 400)
		return
	}

	contrib, err := contribByTourID(tour)
	if err != nil {
		log.Println("joinTournament->contribByTourID: " + err.Error())
		http.Error(res, http.StatusText(400), 400)
		return
	}

	err = tryJoinTournament(tour, partners, contrib/len(partners))
	if err != nil {
		log.Println("joinTournament->tryJoinTournament: " + err.Error())
		http.Error(res, http.StatusText(400), 400)
		return
	}
}

//
// #4 Result tournament winners and prizes
// /resultTournament with a POST document in format: {"tournamentId": "1", "winners": [{"playerId": "P1", "prize": 500}]}
// /resultTournament?tournamentId=1&playerId=P1
func resultTournament(res http.ResponseWriter, req *http.Request) {
	var (
		tour      string
		playerWin string
		err       error
	)
	tour = req.FormValue("tournamentId")
	playerWin = req.FormValue("playerId")

	muxPlayers.Lock()
	defer muxPlayers.Unlock()
	muxJoinings.Lock()
	defer muxJoinings.Unlock()

	if tour == "" {
		log.Println("resultTournament: generate end of tournament...")
		if playerWin != "" {
			tour, err = getTourForWinner(playerWin)
			if err != nil {
				log.Println("resultTournament->getTourForWinner: " + err.Error())
				http.Error(res, http.StatusText(400), 400) //TODO or bad request?
				return
			}
		} else {
			tour, err = getRandomTour()
			if err != nil {
				log.Println("resultTournament->getRandomTour: " + err.Error())
				http.Error(res, http.StatusText(500), 500) //TODO or bad request?
				return
			}
		}
	}

	if playerWin == "" {
		playerWin, err = getRandomWinnerInTour(tour)
		if err != nil {
			log.Println("resultTournament->getRandomWinnerInTour: " + err.Error())
			http.Error(res, http.StatusText(400), 400) //TODO or bad request?
			return
		}
	}

	log.Println(tour, playerWin)

	acquirer, err := getWinnersList(playerWin)
	if err != nil {
		log.Println("resultTournament->getWinnersList: " + err.Error())
		http.Error(res, http.StatusText(500), 500)
		return
	}

	err = setWinner(tour, playerWin)
	if err != nil {
		log.Println("resultTournament->setWinner: " + err.Error())
		http.Error(res, http.StatusText(500), 500)
		return
	}

	prize, err := getPrizeValue(tour)
	if err != nil {
		log.Println("resultTournament->getPrizeValue: " + err.Error())
		http.Error(res, http.StatusText(500), 500)
		return
	}

	profit := prize / len(acquirer)
	var status int

	for _, winner := range acquirer {

		status = updatePlayerBalance(winner, profit)
		if status != 0 {
			log.Println("resultTournament->updatePlayerBalance: error")
			http.Error(res, http.StatusText(status), status)
			return
		}

		err = removePlayerJoin(tour, winner)
		if err != nil {
			log.Println("resultTournament->removePlayerJoin: " + err.Error())
			http.Error(res, http.StatusText(500), 500)
			return
		}
	}

	losers, err := getLosersList(tour)
	if err != nil {
		log.Println("resultTournament->getLosersList: " + err.Error())
		// http.Error(res, http.StatusText(500), 500)
		// return
	} else {
		for _, loser := range losers {
			err = removePlayerJoin(tour, loser)
			if err != nil {
				log.Printf("resultTournament->removePlayerJoin: removing %s from joinings failed: %s\n", loser, err.Error())
				http.Error(res, http.StatusText(500), 500)
				return
			}
		}
	}

	var resulWinner jsonResult
	var victor jsonWinner
	resulWinner.TourID = tour
	victor.PlayerID = playerWin
	victor.Prize = prize
	resulWinner.Winners = append(resulWinner.Winners, victor)

	json.NewEncoder(res).Encode(resulWinner)
}

//
// #5 Player balance
// /balance?playerId=P1 Example response: {"playerId": "P1", "balance": 456.00}
func balance(res http.ResponseWriter, req *http.Request) {
	var item jsonPlayer

	player := req.FormValue("playerId")

	balance, err := getPlayerBalance(player)
	if err == sql.ErrNoRows {
		log.Println("balance->getPlayerBalance: row not found")
		http.Error(res, http.StatusText(404), 404)
		return
	} else if err != nil {
		log.Println("balance->getPlayerBalance: " + err.Error())
		http.Error(res, http.StatusText(500), 500)
		return
	}

	item.Balance = balance
	item.PlayerID = player
	json.NewEncoder(res).Encode(item)
}

//
// #6 Reset DB.
// /reset
func reset(res http.ResponseWriter, req *http.Request) {
	_, err := dbGames.Exec("TRUNCATE players, joinings, tournaments")

	if err != nil {
		log.Println("reset: " + err.Error())
		http.Error(res, http.StatusText(500), 500)
		return
	}
}

//utility functions
func contribByTourID(id string) (int, error) {
	var (
		deposit int
		row     *sql.Row
		err     error
	)

	row = dbGames.QueryRow("SELECT deposit FROM tournaments WHERE tourid = $1", id)
	err = row.Scan(&deposit)

	return deposit, err
}

func getTourStatus(tourid string) error {
	var player sql.NullString
	row := dbGames.QueryRow("SELECT playerid FROM tournaments WHERE tourid = $1", tourid)
	err := row.Scan(&player)
	if player.Valid {
		return fmt.Errorf("tournament %s already finished", tourid)
	}
	fmt.Println(err)
	return err
}

func tryJoinTournament(tourid string, partners []string, fee int) error {
	var (
		playerBalance int
		err           error
	)

	remainders := make([]int, len(partners))

	//check whether player already joined to this tournament
	err = getPlayerJoinStatus(tourid, partners[0])
	if err != nil {
		return err
	}

	for i, player := range partners {
		playerBalance, err = getPlayerBalance(player)
		if err != nil {
			return err
		}

		if playerBalance < fee {
			return fmt.Errorf("player %s have insufficient balance", player)
		}
		remainders[i] = playerBalance - fee
	}

	for i, player := range partners {
		err = setPlayerJoined(tourid, player, fee, partners[0])
		if err != nil {
			return err
		}

		err = setPlayerBalance(player, remainders[i])
		if err != nil {
			return err
		}
	}

	return nil
}

func updatePlayerBalance(player string, points int) int {

	balance, err := getPlayerBalance(player)
	if err == sql.ErrNoRows {
		//create new player
		err = addPlayerAndBalance(player, points)
		if err != nil {
			log.Println(err.Error())
			return 400
		}
		return 0
	} else if err != nil {
		log.Println(err.Error())
		return 500
	}

	err = setPlayerBalance(player, balance+points)
	if err != nil {
		log.Println(err.Error())
		return 400
	}

	return 0
}

func decresePlayerBalance(player string, points int) int {

	balance, err := getPlayerBalance(player)
	if err == sql.ErrNoRows {
		log.Println("row not found")
		return 404
	} else if err != nil {
		log.Println(err.Error())
		return 500
	}

	if balance < points {
		log.Println("insufficient balance")
		return 400
	}

	err = setPlayerBalance(player, balance-points)
	if err != nil {
		log.Println(err.Error())
		return 400
	}
	return 0
}

func getPlayerBalance(player string) (int, error) {
	var balance int
	row := dbGames.QueryRow("SELECT balance FROM players WHERE playerid = $1", player)
	err := row.Scan(&balance)
	return balance, err
}

func setPlayerBalance(player string, balance int) error {
	_, err := dbGames.Exec("UPDATE players SET balance = $1 WHERE playerId = $2", balance, player)
	return err
}

func addPlayerAndBalance(player string, balance int) error {
	_, err := dbGames.Exec("INSERT INTO players VALUES ($1, $2)", player, balance)
	return err
}

func getPlayerJoinStatus(tour string, player string) error {
	var playerID sql.NullString // string
	row := dbGames.QueryRow("SELECT contributeto FROM joinings WHERE tourid = $1 AND playerid = contributeto AND playerid = $2", tour, player)
	err := row.Scan(&playerID)
	// scan for no rows
	if err == sql.ErrNoRows {
		log.Println("no players")
		return nil
	}
	if playerID.Valid {
		return fmt.Errorf("player already joined")
	}
	return err
}

func setPlayerJoined(tourid string, player string, fee int, contributeto string) error {
	_, err := dbGames.Exec("INSERT INTO joinings (tourid, playerid, contribute, contributeto) VALUES ($1,$2,$3,$4)", tourid, player, fee, contributeto)
	return err
}

func removePlayerJoin(tour string, player string) error {
	_, err := dbGames.Exec("DELETE FROM joinings WHERE tourid = $1 AND playerid = $2", tour, player)
	return err
}

func setWinner(tour string, player string) error {
	_, err := dbGames.Exec("UPDATE tournaments SET playerid = $1 WHERE tourid = $2", player, tour)
	return err
}

func getRandomTour() (string, error) {
	tours, err := dbGames.Query("SELECT tourid FROM joinings WHERE playerid = contributeto")
	if err != nil {
		return "", err
	}
	defer tours.Close()

	tour := make([]string, 0)
	var tmp string

	for tours.Next() {
		err = tours.Scan(&tmp)
		tour = append(tour, tmp)
	}
	if len(tour) == 0 {
		return "", fmt.Errorf("there is no joined tournaments")
	}
	return tour[rand.Intn(len(tour))], err
}

func getTourForWinner(player string) (string, error) {
	// calculate winners and theirs profits
	tours, err := dbGames.Query("SELECT tourid FROM joinings WHERE contributeto = $1", player)
	if err != nil {
		return "", nil
	}
	defer tours.Close()

	var tmp string
	tour := make([]string, 0)

	for tours.Next() {
		tours.Scan(&tmp)
		tour = append(tour, tmp)
	}

	if len(tour) == 0 {
		return "", fmt.Errorf("there is no tours for winer")
	}

	return tour[rand.Intn(len(tour))], err
}

func getRandomWinnerInTour(tour string) (string, error) {
	var err error

	err = getTourStatus(tour)
	if err != nil {
		return "", err
	}

	participants, err := dbGames.Query("SELECT playerid FROM joinings WHERE tourid = $1 AND playerid = contributeto", tour)
	if err != nil {
		return "", err
	}
	defer participants.Close()

	participant := make([]string, 0)
	var tmp string
	for participants.Next() {
		err = participants.Scan(&tmp)
		participant = append(participant, tmp)
	}
	if len(participant) == 0 {
		return "", fmt.Errorf("there is no joined players")
	}
	return participant[rand.Intn(len(participant))], err
}

func getPrizeValue(tour string) (int, error) {
	// calculate participants and prize
	participants, err := dbGames.Query("SELECT * FROM joinings WHERE tourid = $1 AND playerid = contributeto", tour)
	if err != nil {
		return 0, err
	}
	defer participants.Close()

	var participantsNumber int
	for participants.Next() {
		participantsNumber++
	}

	row := dbGames.QueryRow("SELECT deposit FROM tournaments WHERE tourid = $1", tour)
	var ante int
	err = row.Scan(&ante)
	if err != nil {
		return 0, err
	}
	return ante * participantsNumber, nil
}

func getWinnersList(player string) ([]string, error) {
	// calculate winners and theirs profits
	acquirers, err := dbGames.Query("SELECT playerid FROM joinings WHERE contributeto = $1", player)
	if err != nil {
		return nil, nil
	}
	defer acquirers.Close()

	var tmp string
	acquirer := make([]string, 0)

	for acquirers.Next() {
		acquirers.Scan(&tmp)
		acquirer = append(acquirer, tmp)
	}
	if len(acquirer) == 0 {
		err = fmt.Errorf("there is no winers")
	}
	return acquirer, err
}

func getLosersList(tour string) ([]string, error) {
	// calculate winners and theirs profits
	losers, err := dbGames.Query("SELECT playerid FROM joinings WHERE tourid = $1", tour)
	if err != nil {
		log.Println(err.Error())
		return nil, err
	}
	defer losers.Close()

	var tmp string
	loser := make([]string, 0)

	for losers.Next() {
		losers.Scan(&tmp)
		loser = append(loser, tmp)
	}
	if len(loser) == 0 {
		err = fmt.Errorf("there is no losers")
	}

	return loser, err
}

func checkTournament(tour string) error {
	//check whether tournament already announced
	var item string
	row := dbGames.QueryRow("SELECT tourid FROM tournaments WHERE tourid = $1", tour)
	err := row.Scan(&item)
	return err
}

func addTournament(tour string, deposit int) error {
	_, err := dbGames.Exec("INSERT INTO tournaments (tourid, deposit) VALUES ($1, $2)", tour, deposit)
	return err
}
