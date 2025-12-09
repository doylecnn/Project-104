// static/js/state.js

let myId = sessionStorage.getItem("take5_uid") || "";
let myName = sessionStorage.getItem("take5_name") || "";
let currentRoomId = "";
let currentGameState = null;
let mySelectedCardValue = null;
let myConfirmPending = false;
let roomStats = [];
let lastSubmittedCardRect = null;
let prevRowsSnapshot = null;
let prevPlayersSnapshot = null;
let gameOverShown = false;

export function getMyId() { return myId; }
export function getMyName() { return myName; }
export function getCurrentRoomId() { return currentRoomId; }
export function getCurrentGameState() { return currentGameState; }
export function getMySelectedCardValue() { return mySelectedCardValue; }
export function getMyConfirmPending() { return myConfirmPending; }
export function getRoomStats() { return roomStats; }
export function getLastSubmittedCardRect() { return lastSubmittedCardRect; }
export function getPrevRowsSnapshot() { return prevRowsSnapshot; }
export function getPrevPlayersSnapshot() { return prevPlayersSnapshot; }

export function setIdentity(id, name) {
    myId = id;
    myName = name;
    sessionStorage.setItem("take5_uid", myId);
    sessionStorage.setItem("take5_name", myName);
}

export function setCurrentRoomId(id) { currentRoomId = id; }
export function setCurrentGameState(state) { currentGameState = state; }
export function setMySelectedCardValue(val) { mySelectedCardValue = val; }
export function setMyConfirmPending(pending) { myConfirmPending = pending; }
export function setRoomStats(stats) { roomStats = stats; }
export function setLastSubmittedCardRect(rect) { lastSubmittedCardRect = rect; }
export function setPrevRowsSnapshot(snap) { prevRowsSnapshot = snap; }
export function setPrevPlayersSnapshot(snap) { prevPlayersSnapshot = snap; }

export function isOwner() {
    return currentGameState && currentGameState.publicState.ownerId === myId;
}

export function setGameOverShown(shown) { gameOverShown = shown; }
export function getGameOverShown() { return gameOverShown; }