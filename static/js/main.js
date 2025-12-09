// static/js/main.js

import { connectLobby, connectGame, sendAction, closeGame, closeLobby } from './network.js';
import * as UI from './ui.js';
import * as State from './state.js';

let lastPendingLogKey = "";

window.onload = function() {
    // Restore session
    const storedId = sessionStorage.getItem("take5_uid");
    const storedName = sessionStorage.getItem("take5_name");
    if (storedId) State.setIdentity(storedId, storedName);
    
    if (State.getMyName()) {
        document.getElementById("username-input").value = State.getMyName();
    }

    const urlParams = new URLSearchParams(window.location.search);
    const roomParam = urlParams.get('room');

    if (roomParam) {
        document.getElementById("new-room-id").value = roomParam;
        if (State.getMyName()) {
            joinRoom(roomParam);
        } else {
            alert("请先输入昵称");
        }
    } else {
        connectLobby(window.location.host);
    }

    // Bind Global Events
    window.createRoom = createRoom;
    window.joinRoom = joinRoom; // for direct calls if any
    window.leaveRoom = leaveRoom;
    window.deleteRoom = deleteRoom;
    window.sendReady = sendReady;
    window.sendRestart = sendRestart;
    window.sendForceRestart = sendForceRestart;
    window.confirmPlay = confirmPlay;
    window.showStats = showStats;
    window.closeStats = closeStats;
    window.copyInviteLink = copyInviteLink;

    // Listen for custom events from UI module
    window.addEventListener('join-room', (e) => joinRoom(e.detail));
};

// --- Actions ---

function saveUserInfo() {
    const nameInput = document.getElementById("username-input");
    const name = nameInput.value.trim();
    if (!name) { alert("请输入昵称"); return false; }
    State.setIdentity(State.getMyId(), name);
    return true;
}

async function createRoom() {
    if (!saveUserInfo()) return;
    const roomId = document.getElementById("new-room-id").value.trim();
    if (!roomId) return alert("请输入房间号");
    connectGame(window.location.host, roomId, "create_room", State.getMyId(), State.getMyName());
}

async function joinRoom(roomId) {
    if (!saveUserInfo()) return;
    try {
        const res = await fetch(`/check_room?id=${roomId}`);
        const data = await res.json();
        if (!data.exists) {
            alert("房间不存在！");
            window.history.pushState({}, document.title, "/");
            connectLobby(window.location.host);
            return;
        }
        connectGame(window.location.host, roomId, "login", State.getMyId(), State.getMyName());
    } catch (e) {
        alert("网络错误");
    }
}

export function leaveRoom(passive = false) {
    if (!passive) {
        sendAction({ type: "leave_room" });
    }
    closeGame();
    State.setCurrentRoomId("");
    window.history.pushState({}, document.title, "/");
    switchScreen("lobby");
    connectLobby(window.location.host);
}

function deleteRoom() {
    if (confirm("确定要解散房间吗？所有玩家将被踢出。")) {
        sendAction({ type: "delete_room" });
    }
}

function sendReady() { sendAction({type: "ready"}); }
function sendRestart() { sendAction({type: "restart"}); }
function sendForceRestart() {
    if (confirm("确定要强制重开一局新游戏吗？本局将被作废。")) {
        sendAction({type: "force_restart"});
    }
}

export function confirmPlay() {
    const val = State.getMySelectedCardValue();
    if (val === null) return;
    
    // Capture rect for animation
    const selEl = document.querySelector("#hand .card.selected");
    if (selEl) {
        const rect = selEl.getBoundingClientRect();
        State.setLastSubmittedCardRect({
            left: rect.left, top: rect.top, width: rect.width, height: rect.height
        });
    }

    sendAction({type: "play_card", value: val});
    State.setMyConfirmPending(true);
    
    const handEl = document.getElementById("hand");
    if (handEl) handEl.classList.add("locked");
    
    UI.updateConfirmButton("playing", true, true);
    UI.log(`你已提交出牌 ${val}，等待其他玩家确认...`);
}

function copyInviteLink() {
    const url = window.location.href;
    navigator.clipboard.writeText(url).then(() => {
        alert("链接已复制: " + url);
    });
}

function showStats() { 
    document.getElementById("stats-modal").style.display = "flex"; 
    UI.renderStats(); 
}
function closeStats() { 
    document.getElementById("stats-modal").style.display = "none"; 
}

// --- Logic & Rendering Wiring ---

export function handleStateUpdate(msg) {
    if (msg.type === "auto_restart_countdown") {
        UI.updateInstructions("countdown", null, State.getMyId(), msg.payload.Count); // Pass countdown
        return;
    } else if (msg.type === "state") {
        const payload = msg.payload;
        // Switch screen if needed
        if (document.getElementById("lobby-screen").style.display !== "none") {
            switchScreen("game");
            const url = new URL(window.location);
            url.searchParams.set('room', payload.roomId);
            window.history.pushState({}, '', url);
            closeLobby();
        }

        State.setCurrentGameState(payload);
        State.setCurrentRoomId(payload.roomId);
        
        const publicState = payload.publicState;
        const myHand = payload.myHand || [];
        const status = publicState.status;
        const me = publicState.players[State.getMyId()] || {};

        // Sync selected card state
        if (payload.mySelectedCard !== undefined && payload.mySelectedCard !== null) {
            State.setMySelectedCardValue(payload.mySelectedCard);
            State.setMyConfirmPending(true);
        }

        const iHaveSelected = !!me.hasSelected;

        // Reset pending state if round changed or not playing
        if (!iHaveSelected && status === "playing") {
             State.setMyConfirmPending(false);
        }
        if (status !== "playing" && status !== "choosing_row") {
            State.setMyConfirmPending(false);
            State.setMySelectedCardValue(null);
        }

        // Validate selected card is still in hand
        const handValues = myHand.map(c => c.value);
        if (State.getMySelectedCardValue() !== null && !handValues.includes(State.getMySelectedCardValue()) && !State.getMyConfirmPending()) {
            State.setMySelectedCardValue(null);
        }

        // Update UI elements
        document.getElementById("current-room-id").innerText = payload.roomId;
        
        const isOwnerVal = (publicState.ownerId === State.getMyId());
        document.getElementById("delete-btn").style.display = isOwnerVal ? "inline-block" : "none";
        document.getElementById("restart-btn").style.display = (isOwnerVal && status === "finished") ? "inline-block" : "none";
        
            // Check for offline players for force restart button visibility
            const hasOffline = Object.values(publicState.players).some(p => !p.isOnline);
            document.getElementById("force-restart-btn").style.display = (isOwnerVal && status === "playing" && hasOffline) ? "inline-block" : "none";
        
                    UI.renderPlayers(publicState.players, publicState.pendingPlayerId, publicState.ownerId);
        
                    
        
                    const isLockedForHand = State.getMyConfirmPending() || iHaveSelected || status !== "playing";
        
                    
        
                    // Define card click handler
        
                    const onCardClick = (val) => {
        
                        // Re-check lock state inside handler to be safe, though UI should prevent clicks
        
                        const currentIsLocked = State.getMyConfirmPending() || iHaveSelected || status !== "playing";
        
                        if (currentIsLocked) return;
        
                
        
                        if (State.getMySelectedCardValue() === val) {
        
                            State.setMySelectedCardValue(null);
        
                        } else {
        
                            State.setMySelectedCardValue(val);
        
                        }
        
                        
        
                        // Re-run prediction and updates
        
                        const currentGameState = State.getCurrentGameState();
        
                        const predictedRowIdx = predictRow(State.getMySelectedCardValue(), currentGameState ? currentGameState.publicState.rows : [], status);
        
                        UI.renderPredictionMessage(predictedRowIdx);
        
                        
        
                        if (currentGameState) {
        
                            UI.renderBoard(currentGameState.publicState.rows, status, currentGameState.publicState.pendingPlayerId, predictedRowIdx, new Map(), State.getMyId());
        
                        }
        
                        UI.updateConfirmButton(status, iHaveSelected, State.getMyConfirmPending());
        
                        
        
                        // Re-render hand with current lock state
        
                        UI.renderHand(myHand, currentIsLocked, onCardClick);
        
                    };
        
                
        
                    UI.renderHand(myHand, isLockedForHand, onCardClick); 
        
                    UI.updateConfirmButton(status, iHaveSelected, State.getMyConfirmPending());
        
                
        
                    const predictedRowIdx = predictRow(State.getMySelectedCardValue(), publicState.rows, status);
        
                    UI.renderPredictionMessage(predictedRowIdx);
        
                
        
                    // Calc Diff for Animation and Logging
        
                    const landingMap = computeLanding(State.getPrevRowsSnapshot(), publicState.rows);
    const landingAll = flattenLanding(landingMap);
    const playEvents = diffPlayerPlays(State.getPrevPlayersSnapshot(), publicState.players, landingAll);
    
    const cardOwnerMap = {};
    playEvents.forEach(ev => { if(ev.cardValue) cardOwnerMap[ev.cardValue] = ev.id; });

    const animationTargets = UI.renderBoard(publicState.rows, status, publicState.pendingPlayerId, predictedRowIdx, landingMap, State.getMyId());

    if (animationTargets.length > 0) {
        UI.processAnimations(animationTargets, cardOwnerMap, State.getMyId(), State.getLastSubmittedCardRect());
        State.setLastSubmittedCardRect(null); // Consumed
    }

    playEvents.forEach(ev => {
        if (ev.cardValue !== null) {
            const rowIdx = findLandingRow(landingMap, ev.cardValue);
            const rowText = rowIdx !== null ? `，落在第 ${rowIdx + 1} 行` : "";
            UI.log(`${ev.name} 出牌 ${ev.cardValue}${rowText}`);
        }
    });

    State.setPrevRowsSnapshot(snapshotRows(publicState.rows));
    State.setPrevPlayersSnapshot(snapshotPlayers(publicState.players));

    // Pending Play Log
    if (publicState.pendingCard && publicState.pendingPlayerId) {
        const key = `${publicState.pendingPlayerId}-${publicState.pendingCard.value}`;
        if (key !== lastPendingLogKey) {
            lastPendingLogKey = key;
            const p = publicState.players[publicState.pendingPlayerId];
            const name = p ? p.name : "未知玩家";
            UI.log(`${name} 的出牌 ${publicState.pendingCard.value} 等待处理`);
        }
    }
    
    UI.updateInstructions(status, publicState, State.getMyId());
}
}

// Re-implemented helper functions locally or imported where it makes sense
// Ideally these are in a 'logic.js' or 'utils.js' but fitting in main or UI for now.

function switchScreen(screen) {
    document.querySelectorAll('.screen').forEach(el => el.classList.remove('active'));
    document.getElementById(screen + '-screen').classList.add('active');
}

export function renderRoomList(rooms) {
    UI.renderRoomList(rooms);
}

export function log(msg) {
    UI.log(msg);
}

// --- Prediction & Diff Helpers ---

function predictRow(value, rows, status) {
    if (status !== "playing" || value === null || !rows) return null;
    let bestIdx = -1;
    let diff = 1e9;
    rows.forEach((row, idx) => {
        if (!row.cards || row.cards.length === 0) return;
        const last = row.cards[row.cards.length - 1].value;
        if (value > last && value - last < diff) {
            diff = value - last;
            bestIdx = idx;
        }
    });
    return bestIdx === -1 ? -1 : bestIdx;
}

function snapshotRows(rows) { return rows ? rows.map(r => r.cards ? r.cards.map(c => c.value) : []) : null; }
function computeLanding(prev, current) {
    const res = new Map();
    if (!prev) return res;
    current.forEach((row, idx) => {
        const prevVals = prev[idx] || [];
        const added = (row.cards || []).filter(c => !prevVals.includes(c.value));
        if (added.length > 0) res.set(idx, added);
    });
    return res;
}
function flattenLanding(landingMap) {
    const arr = [];
    landingMap.forEach(cards => cards.forEach(c => arr.push(c)));
    return arr;
}
function snapshotPlayers(players) {
    if (!players) return null;
    const res = {};
    Object.values(players).forEach(p => { res[p.id] = { handSize: p.handSize ?? 0 }; });
    return res;
}
function diffPlayerPlays(prevSnap, currPlayers, landingCards) {
    const events = [];
    landingCards.forEach(card => {
        if (card.ownerId && currPlayers[card.ownerId]) {
            const p = currPlayers[card.ownerId];
            events.push({ id: p.id, name: p.name, cardValue: card.value });
        }
    });
    return events;
}
function findLandingRow(landingMap, val) {
    let hit = null;
    landingMap.forEach((cards, idx) => { if (cards.some(c => c.value === val)) hit = idx; });
    return hit;
}
