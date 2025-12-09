import { getMyId, getMySelectedCardValue, getRoomStats, isOwner, setMySelectedCardValue } from './state.js';
import { sendAction } from './network.js';

export function renderRoomList(rooms) {
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
        // We need to call a function in main.js to handle join logic
        div.onclick = () => window.dispatchEvent(new CustomEvent('join-room', { detail: r.id }));
        container.appendChild(div);
    });
}

// ... (renderPlayers, createCard, renderBoard functions remain unchanged)

export function renderPlayers(players, pendingPid, ownerId) {
    const container = document.getElementById("player-list");
    container.innerHTML = "";
    const myId = getMyId();
    Object.values(players).forEach(p => {
        const div = document.createElement("div");
        div.className = `player-tag ${p.id === myId ? 'me' : ''} ${p.ready ? 'ready' : ''} ${p.id === ownerId ? 'owner' : ''} ${p.isOnline ? 'online' : 'offline'}`;
        div.dataset.uid = p.id; 
        div.innerText = `${p.name} (${p.score})`;
        container.appendChild(div);
    });
}

export function createCard(c) {
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

export function renderBoard(rows, status, pendingPid, predictedRow, landingMap, myId) {
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
        if (isMyTurn) div.onclick = () => { 
            if(confirm("收走此行?")) sendAction({type:"choose_row", value: idx}); 
        };
        
        const landingCards = landingMap.get(idx) || [];
        
        // Calculate and display row score (bullheads)
        const rowScore = (row.cards || []).reduce((sum, c) => sum + c.score, 0);
        const scoreDiv = document.createElement("div");
        scoreDiv.className = "row-score";
        scoreDiv.innerHTML = `${rowScore} 🐮`;
        div.appendChild(scoreDiv);

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

export function updateInstructions(status, publicState, myId, countdownValue = null) {
    const instruction = document.getElementById("instruction");
    const readyBtn = document.getElementById("ready-btn");
    
    if (status === "countdown" && countdownValue !== null) {
        instruction.style.display = "block";
        instruction.style.background = "#e67e22"; // Orange for countdown
        instruction.innerHTML = `新一局游戏将在 <strong>${countdownValue}</strong> 秒后开始...`;
        readyBtn.style.display = "none"; // Hide ready button during countdown
        return;
    }

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

export function updateConfirmButton(status, iHaveSelected, myConfirmPending) {
    const btn = document.getElementById("btn-confirm-play");
    if (!btn) return;
    const shouldEnable = status === "playing" && !iHaveSelected && !myConfirmPending && getMySelectedCardValue() !== null;
    btn.disabled = !shouldEnable;
}

export function renderPredictionMessage(idx) {
    const el = document.getElementById("prediction-msg");
    if (!el) return;
    if (idx === null) el.innerText = "";
    else if (idx === -1) el.innerText = "这张牌可能触发收行，请谨慎选择。";
    else el.innerText = `预计将放入第 ${idx + 1} 行`;
}

export function renderStats() {
    const tbody = document.getElementById("stats-body");
    tbody.innerHTML = "";
    getRoomStats().forEach((s, i) => {
        tbody.innerHTML += `<tr><td>${i+1}</td><td>${s.name}</td><td>${s.totalGames}</td><td>${s.totalScore}</td></tr>`;
    });
}

export function processAnimations(targets, cardOwnerMap, myId, lastSubmittedCardRect) {
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
}

export function renderHand(hand, isLocked, onCardClick) {
    const container = document.getElementById("hand");
    container.innerHTML = "";
    container.className = `hand ${isLocked ? 'locked' : ''}`;

    hand.forEach(c => {
        const el = createCard(c);
        if (getMySelectedCardValue() === c.value) {
            el.classList.add("selected");
        }
        el.onclick = () => {
            if (!isLocked && onCardClick) onCardClick(c.value);
        };
        container.appendChild(el);
    });
}

export function log(msg) {
    const d = document.getElementById("log");
    d.innerHTML = `<div>${msg}</div>` + d.innerHTML;
}

export function renderGameOver(players, myId) {
    const modal = document.getElementById("game-over-modal");
    const container = document.getElementById("game-over-stats");
    
    // 按得分排序（低分在前）
    const sortedPlayers = Object.values(players).sort((a, b) => a.score - b.score);
    
    container.innerHTML = "";
    sortedPlayers.forEach((p, i) => {
        const div = document.createElement("div");
        div.className = `game-over-player ${p.id === myId ? 'me' : ''} ${i === 0 ? 'winner' : ''}`;
        
        // 排名图标
        const rankIcon = ['🥇', '🥈', '🥉', '4️⃣', '5️⃣', '6️⃣', '7️⃣', '8️⃣', '9️⃣', '🔟'][i] || '📝';
        
        div.innerHTML = `
            <div>
                <span style="font-size: 20px; margin-right: 10px;">${rankIcon}</span>
                <span style="font-weight: bold;">${p.name}</span>
                ${p.id === myId ? '<span style="color: #2980b9; margin-left: 5px;">(我)</span>' : ''}
                ${i === 0 ? '<span style="color: #f1c40f; margin-left: 5px;">🏆 胜利</span>' : ''}
            </div>
            <div style="font-size: 20px; font-weight: bold; color: #e74c3c;">
                ${p.score} 🐮
            </div>
        `;
        container.appendChild(div);
    });
    
    modal.style.display = "flex";
}

export function closeGameOver() {
    document.getElementById("game-over-modal").style.display = "none";
}

export function updateCountdownDisplay(count) {
    const el = document.getElementById("countdown-display");
    if (el) {
        el.innerHTML = `⏱️ 新一局游戏将在 <strong>${count}</strong> 秒后开始...`;
    }
}