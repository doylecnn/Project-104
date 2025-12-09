// static/js/network.js

import { handleStateUpdate, renderRoomList, log } from './main.js';

let lobbyWs;
let gameWs;

export function connectLobby(protocol, host) {
    if (lobbyWs) return;
    let scheme = "wss://"
    if(protocol==="http:"){
        scheme = "ws://"
    }
    lobbyWs = new WebSocket(scheme + host + "/lobby_ws");
    lobbyWs.onmessage = (evt) => {
        const msg = JSON.parse(evt.data);
        if (msg.type === "room_list") {
            renderRoomList(msg.payload);
        }
    };
}

export function connectGame(protocol, host, roomId, actionType, myId, myName) {
    if (gameWs) gameWs.close();
    let scheme = "wss://"
    if(protocol==="http:"){
        scheme = "ws://"
    }
    gameWs = new WebSocket(scheme + host + "/ws");
    
    gameWs.onopen = () => {
        sendAction({
            type: actionType,
            id: myId,
            payload: myName,
            roomId: roomId
        });
    };

    gameWs.onmessage = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.type === "identity") {
            // Callback to update local state with confirmed ID/Name
            import('./state.js').then(module => {
                module.setIdentity(msg.payload.id, msg.payload.name);
            });
            console.log("Identity confirmed:", msg.payload.name, msg.payload.id);
        } else if (msg.type === "error") {
            alert(msg.payload);
            gameWs.close();
            gameWs = null;
        } else if (msg.type === "state" || msg.type === "auto_restart_countdown") {
            handleStateUpdate(msg);
        } else if (msg.type === "info") {
            log(msg.payload);
        } else if (msg.type === "stats") {
            import('./state.js').then(module => {
                module.setRoomStats(msg.payload || []);
            });
            // Ideally UI update should be triggered here, but for now main.js handles renders
            import('./ui.js').then(module => {
                module.renderStats();
            });
        } else if (msg.type === "room_closed") {
            alert("房间已解散");
            import('./main.js').then(module => {
                module.leaveRoom(true); 
            });
        }
    };

    gameWs.onclose = () => {
        console.log("Game connection closed");
    };
}

export function sendAction(action) {
    if (gameWs && gameWs.readyState === WebSocket.OPEN) {
        gameWs.send(JSON.stringify(action));
    } else {
        console.error("WebSocket is not open");
    }
}

export function closeGame() {
    if (gameWs) {
        gameWs.close();
        gameWs = null;
    }
}

export function closeLobby() {
    if (lobbyWs) {
        lobbyWs.close();
        lobbyWs = null;
    }
}
