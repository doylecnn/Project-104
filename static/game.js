let ws;
let lobbyWs;
let myId = sessionStorage.getItem("take5_uid") || ""; 
let myName = sessionStorage.getItem("take5_name") || "";
let currentRoomId = "";
let isOwner = false;
let roomStats = [];
let mySelectedCardValue = null;
let currentGameState = null;
let myConfirmPending = false;
let lastPendingLogKey = "";
let predictedRowIdx = null;
let prevRowsSnapshot = null;
let prevPlayersSnapshot = null;
let lastSubmittedCardRect = null;

window.onload = function() {
    sessionStorage.setItem("take5_uid", myId);
    if (myName) document.getElementById("username-input").value = myName;

    const urlParams = new URLSearchParams(window.location.search);
    const roomParam = urlParams.get('room');

    if (roomParam) {
        document.getElementById("new-room-id").value = roomParam;
        if (myName) {
            joinRoom(roomParam);
        } else {
            alert("请先输入昵称");
        }
    } else {
        connectLobby();
    }
};

function connectLobby() {
    if (lobbyWs) return;
    lobbyWs = new WebSocket("ws://" + window.location.host + "/lobby_ws");
    lobbyWs.onmessage = (evt) => {
        const msg = JSON.parse(evt.data);
        if (msg.type === "room_list") {
            renderRoomList(msg.payload);
        }
    };
}

function renderRoomList(rooms) {
    const container = document.getElementById("room-container");
    container.innerHTML = "";
    if (rooms.length === 0) {
        container.innerHTML = "<div style='text-align:center; padding:10px; color:#666;'>暂无房间，快创建一个吧！</div>";
        return;
    }
    rooms.forEach(r => {
        const div = document.createElement("div");
        div.className = "room-item";
        div.innerHTML = `
            <div class="room-info">
                <strong>房间 ${r.id}</strong> <span style="color:#666">(${r.ownerName})</span>
                <br>人数: ${r.playerCount}
            </div>
            <div class="room-status ${r.status}">${r.status === 'waiting' ? '等待中' : '游戏中'}</div>
        `;
        div.onclick = () => joinRoom(r.id);
        container.appendChild(div);
    });
}

function saveUserInfo() {
    const nameInput = document.getElementById("username-input");
    myName = nameInput.value.trim();
    if (!myName) { alert("请输入昵称"); return false; }
    sessionStorage.setItem("take5_name", myName);
    return true;
}

async function createRoom() {
    if (!saveUserInfo()) return;
    const roomId = document.getElementById("new-room-id").value.trim();
    if (!roomId) return alert("请输入房间号");
    connectGameWs(roomId, "create_room");
}

async function joinRoom(roomId) {
    if (!saveUserInfo()) return;
    try {
        const res = await fetch(`/check_room?id=${roomId}`);
        const data = await res.json();
        if (!data.exists) {
            alert("房间不存在！");
            window.history.pushState({}, document.title, "/");
            connectLobby();
            return;
        }
        connectGameWs(roomId, "login");
    } catch (e) {
        alert("网络错误");
    }
}

function connectGameWs(roomId, actionType) {
    if (ws) ws.close();
    ws = new WebSocket("ws://" + window.location.host + "/ws");
    
    ws.onopen = () => {
        ws.send(JSON.stringify({
            type: actionType,
            id: myId,
            payload: myName,
            roomId: roomId
        }));
    };

    ws.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.type === "identity") {
            myId = msg.payload.id;
            myName = msg.payload.name;
            sessionStorage.setItem("take5_uid", myId);
            sessionStorage.setItem("take5_name", myName);
            console.log("Identity confirmed:", myName, myId);
        } else if (msg.type === "error") {
            alert(msg.payload);
            ws.close();
            ws = null;
        } else if (msg.type === "state") {
            if (document.getElementById("lobby-screen").style.display !== "none") {
                switchScreen("game");
                const url = new URL(window.location);
                url.searchParams.set('room', roomId);
                window.history.pushState({}, '', url);
                if (lobbyWs) { lobbyWs.close(); lobbyWs = null; }
            }
            handleStateUpdate(msg.payload);
        } else if (msg.type === "info") {
            log(msg.payload);
        } else if (msg.type === "stats") {
            roomStats = msg.payload || [];
            renderStats();
        } else if (msg.type === "room_closed") {
            alert("房间已解散");
            leaveRoom(true); 
        }
    };

    ws.onclose = () => {
        console.log("Game connection closed");
    };
}

function switchScreen(screen) {
    document.querySelectorAll('.screen').forEach(el => el.classList.remove('active'));
    document.getElementById(screen + '-screen').classList.add('active');
}

function leaveRoom(passive = false) {
    if (!passive && ws) {
        ws.send(JSON.stringify({ type: "leave_room" }));
    }
    if (ws) ws.close();
    ws = null;
    currentRoomId = "";
    window.history.pushState({}, document.title, "/");
    switchScreen("lobby");
    connectLobby();
}

function deleteRoom() {
    if (confirm("确定要解散房间吗？所有玩家将被踢出。")) {
        ws.send(JSON.stringify({ type: "delete_room" }));
    }
}

function copyInviteLink() {
    const url = window.location.href;
    navigator.clipboard.writeText(url).then(() => {
        alert("链接已复制: " + url);
    });
}

function hasOfflinePlayers(players) {
    return Object.values(players).some(p => !p.isOnline);
}

function handleStateUpdate(payload) {
    currentGameState = payload;
    const publicState = payload.publicState;
    const myHand = payload.myHand || [];
    const status = publicState.status;
    const me = publicState.players[myId] || {};
    if (payload.mySelectedCard !== undefined && payload.mySelectedCard !== null) {
        mySelectedCardValue = payload.mySelectedCard;
        myConfirmPending = true; // 标记为已确认，锁定手牌
    }

    const iHaveSelected = !!me.hasSelected;

    if (!iHaveSelected && status === "playing") {
            myConfirmPending = false;
            if (!myConfirmPending) {
                // 只有在非等待确认状态下才清除，避免闪烁
            }
    }
    if (status !== "playing" && status !== "choosing_row") {
        myConfirmPending = false;
        mySelectedCardValue = null;
    }

    const handValues = myHand.map(c => c.value);
    if (mySelectedCardValue !== null && !handValues.includes(mySelectedCardValue) && !myConfirmPending) {
        mySelectedCardValue = null;
    }

    currentRoomId = payload.roomId;
    document.getElementById("current-room-id").innerText = currentRoomId;

    isOwner = (publicState.ownerId === myId);
    document.getElementById("delete-btn").style.display = isOwner ? "inline-block" : "none";
    document.getElementById("restart-btn").style.display = (isOwner && status === "finished") ? "inline-block" : "none";
    // Only show force restart if owner, game is playing, AND there are offline players
    document.getElementById("force-restart-btn").style.display = (isOwner && status === "playing" && hasOfflinePlayers(publicState.players)) ? "inline-block" : "none";

    renderPlayers(publicState.players, publicState.pendingPlayerId, publicState.ownerId);
    renderHand(myHand, status, iHaveSelected);
    updateConfirmButton(status, iHaveSelected);

    predictedRowIdx = predictRow(mySelectedCardValue, publicState.rows, status);
    renderPredictionMessage(predictedRowIdx);

    const landingMap = computeLanding(prevRowsSnapshot, publicState.rows);
    const landingAll = flattenLanding(landingMap);
    const playEvents = diffPlayerPlays(prevPlayersSnapshot, publicState.players, landingAll);
    
    const cardOwnerMap = {};
    playEvents.forEach(ev => { if(ev.cardValue) cardOwnerMap[ev.cardValue] = ev.id; });

    const animationTargets = renderBoard(publicState.rows, status, publicState.pendingPlayerId, predictedRowIdx, landingMap);

    if (animationTargets.length > 0) {
        processAnimations(animationTargets, cardOwnerMap);
    }

    playEvents.forEach(ev => {
        if (ev.cardValue !== null) {
            const rowIdx = findLandingRow(landingMap, ev.cardValue);
            const rowText = rowIdx !== null ? `，落在第 ${rowIdx + 1} 行` : "";
            log(`${ev.name} 出牌 ${ev.cardValue}${rowText}`);
        }
    });
    prevRowsSnapshot = snapshotRows(publicState.rows);
    prevPlayersSnapshot = snapshotPlayers(publicState.players);

    if (publicState.pendingCard && publicState.pendingPlayerId) {
        const key = `${publicState.pendingPlayerId}-${publicState.pendingCard.value}`;
        if (key !== lastPendingLogKey) {
            lastPendingLogKey = key;
            const p = publicState.players[publicState.pendingPlayerId];
            const name = p ? p.name : "未知玩家";
            log(`${name} 的出牌 ${publicState.pendingCard.value} 等待处理`);
        }
    }
    
    updateInstructions(status, publicState);
}

function renderPlayers(players, pendingPid, ownerId) {
    const container = document.getElementById("player-list");
    container.innerHTML = "";
    Object.values(players).forEach(p => {
        const div = document.createElement("div");
        div.className = `player-tag ${p.id === myId ? 'me' : ''} ${p.ready ? 'ready' : ''} ${p.id === ownerId ? 'owner' : ''} ${p.isOnline ? 'online' : 'offline'}`;
        div.dataset.uid = p.id; 
        div.innerText = `${p.name} (${p.score})`;
        container.appendChild(div);
    });
}

function renderBoard(rows, status, pendingPid, predictedRow, landingMap) {
    const board = document.getElementById("board");
    board.innerHTML = "";
    const isMyTurn = (status === "choosing_row" && pendingPid === myId);
    const animationTargets = [];
    
    rows.forEach((row, idx) => {
        const div = document.createElement("div");
        const classes = ["row"];
        if (isMyTurn) classes.push("selectable");
        if (predictedRow === idx) classes.push("predicted");
        div.className = classes.join(" ");
        div.dataset.rowIdx = idx;
        if (isMyTurn) div.onclick = () => { if(confirm("收走此行?")) ws.send(JSON.stringify({type:"choose_row", value: idx})); };
        
        const landingCards = landingMap.get(idx) || [];
        
        (row.cards || []).forEach(c => {
            const cardEl = createCard(c);
            cardEl.dataset.cardValue = c.value;
            const isNew = landingCards.some(lc => lc.value === c.value);
            if (isNew) {
                cardEl.style.opacity = "0"; 
                animationTargets.push({ element: cardEl, value: c.value });
            } else {
                cardEl.style.opacity = "1";
            }
            div.appendChild(cardEl);
        });
        board.appendChild(div);
    });
    return animationTargets;
}

function processAnimations(targets, cardOwnerMap) {
    document.body.offsetHeight; 
    targets.forEach(target => {
        const cardEl = target.element;
        const value = target.value;
        const ownerId = cardOwnerMap[value];
        const endRect = cardEl.getBoundingClientRect();
        let startRect = null;

        if (ownerId === myId) {
            if (lastSubmittedCardRect) startRect = lastSubmittedCardRect;
        } else if (ownerId) {
            const playerEl = document.querySelector(`.player-tag[data-uid="${ownerId}"]`);
            if (playerEl) startRect = playerEl.getBoundingClientRect();
        }

        if (!startRect) {
            cardEl.style.opacity = "1";
            return;
        }

        const ghost = cardEl.cloneNode(true);
        ghost.style.transition = "none"; 
        ghost.style.opacity = "1";
        ghost.style.position = "fixed";
        ghost.style.margin = "0";
        ghost.style.zIndex = "3000";
        ghost.style.pointerEvents = "none";
        
        const startCenterX = startRect.left + startRect.width / 2;
        const startCenterY = startRect.top + startRect.height / 2;
        const cardWidth = endRect.width;
        const cardHeight = endRect.height;
        const initialLeft = startCenterX - cardWidth / 2;
        const initialTop = startCenterY - cardHeight / 2;

        ghost.style.left = `${initialLeft}px`;
        ghost.style.top = `${initialTop}px`;
        ghost.style.width = `${cardWidth}px`;
        ghost.style.height = `${cardHeight}px`;
        ghost.style.transform = "translate(0, 0) scale(0.5)";

        document.body.appendChild(ghost);

        void ghost.offsetWidth;
        requestAnimationFrame(() => {
            ghost.style.transition = "transform 0.6s cubic-bezier(0.2, 0.8, 0.2, 1)";
            requestAnimationFrame(() => {
                const deltaX = endRect.left - initialLeft;
                const deltaY = endRect.top - initialTop;
                ghost.style.transform = `translate(${deltaX}px, ${deltaY}px) scale(1)`;
            });
        });

        setTimeout(() => {
            ghost.remove();
            cardEl.style.opacity = "1";
            cardEl.classList.add("landing");
            setTimeout(() => cardEl.classList.remove("landing"), 500);
        }, 650);
    });
    lastSubmittedCardRect = null;
}

function renderHand(hand, status, iHaveSelected) {
    const container = document.getElementById("hand");
    container.innerHTML = "";
    const isLocked = myConfirmPending || iHaveSelected || status !== "playing";
    container.className = `hand ${isLocked ? 'locked' : ''}`;

    hand.forEach(c => {
        const el = createCard(c);
        if (mySelectedCardValue === c.value) {
            el.classList.add("selected");
        }
        el.onclick = (e) => {
            if (isLocked) return;
            if (mySelectedCardValue === c.value) {
                mySelectedCardValue = null;
            } else {
                mySelectedCardValue = c.value;
            }
            predictedRowIdx = predictRow(mySelectedCardValue, currentGameState ? currentGameState.publicState.rows : [], status);
            renderPredictionMessage(predictedRowIdx);
            if (currentGameState) {
                renderBoard(currentGameState.publicState.rows, status, currentGameState.publicState.pendingPlayerId, predictedRowIdx, new Map());
            }
            updateConfirmButton(status, iHaveSelected);
            renderHand(hand, status, iHaveSelected);
        };
        container.appendChild(el);
    });
}

function confirmPlay() {
    if (mySelectedCardValue === null) return;
    const selEl = document.querySelector("#hand .card.selected");
    if (selEl) {
        const rect = selEl.getBoundingClientRect();
        lastSubmittedCardRect = {
            left: rect.left, top: rect.top, width: rect.width, height: rect.height
        };
    }
    ws.send(JSON.stringify({type: "play_card", value: mySelectedCardValue}));
    myConfirmPending = true;
    const handEl = document.getElementById("hand");
    if (handEl) handEl.classList.add("locked");
    updateConfirmButton("playing", true);
    log(`你已提交出牌 ${mySelectedCardValue}，等待其他玩家确认...`);
}

function createCard(c) {
    const div = document.createElement("div");
    let scoreClass = "score-1";
    if (c.score === 2) scoreClass = "score-2";
    if (c.score === 3) scoreClass = "score-3";
    if (c.score === 5) scoreClass = "score-5";
    if (c.score === 7) scoreClass = "score-7";
    div.className = `card ${scoreClass}`;
    div.innerHTML = `<span style="margin-top:2px">${c.value}</span><div class="bullheads">${'🐮'.repeat(c.score > 3 ? 3 : c.score)}</div>`;
    return div;
}

function updateConfirmButton(status, iHaveSelected) {
    const btn = document.getElementById("btn-confirm-play");
    if (!btn) return;
    const shouldEnable = status === "playing" && !iHaveSelected && !myConfirmPending && mySelectedCardValue !== null;
    btn.disabled = !shouldEnable;
}

function updateInstructions(status, publicState) {
    const instruction = document.getElementById("instruction");
    const readyBtn = document.getElementById("ready-btn");
    if (status === "waiting") {
        readyBtn.style.display = "inline-block";
        const meReady = publicState.players[myId];
        readyBtn.innerText = (meReady && meReady.ready) ? "等待其他人..." : "准备 / Ready";
        readyBtn.disabled = (meReady && meReady.ready);
        instruction.style.display = "none";
    } else {
        readyBtn.style.display = "none";
        if (status === "choosing_row") {
            instruction.style.display = "block";
            if (publicState.pendingPlayerId === myId) {
                instruction.style.background = "#e74c3c";
                instruction.innerHTML = `⚠️ 请选择一行收走！`;
            } else {
                instruction.style.background = "#d35400";
                instruction.innerText = `等待某人选行...`;
            }
        } else {
            instruction.style.display = "none";
        }
    }
}

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

function renderPredictionMessage(idx) {
    const el = document.getElementById("prediction-msg");
    if (!el) return;
    if (idx === null) el.innerText = "";
    else if (idx === -1) el.innerText = "这张牌可能触发收行，请谨慎选择。";
    else el.innerText = `预计将放入第 ${idx + 1} 行`;
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
function sendReady() { ws.send(JSON.stringify({type: "ready"})); }
function sendRestart() { ws.send(JSON.stringify({type: "restart"})); }
function sendForceRestart() {
    if (confirm("确定要强制重开一局新游戏吗？本局将被作废。")) {
        ws.send(JSON.stringify({type: "force_restart"}));
    }
}
function showStats() { document.getElementById("stats-modal").style.display = "flex"; renderStats(); }
function closeStats() { document.getElementById("stats-modal").style.display = "none"; }
function renderStats() {
    const tbody = document.getElementById("stats-body");
    tbody.innerHTML = "";
    roomStats.forEach((s, i) => {
        tbody.innerHTML += `<tr><td>${i+1}</td><td>${s.name}</td><td>${s.totalGames}</td><td>${s.totalScore}</td></tr>`;
    });
}
function log(msg) {
    const d = document.getElementById("log");
    d.innerHTML = `<div>${msg}</div>` + d.innerHTML;
}
