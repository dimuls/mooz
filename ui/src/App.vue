<style>
html,
body,
#app {
  margin: 0;
  padding: 0;
  width: 100%;
  height: 100%;
}
#app {
  display: flex;
  justify-content: stretch;
  align-items: stretch;
}
.container {
  flex-grow: 1;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(1em, 1fr));
  grid-auto-flow: column;
  align-items: stretch;
  justify-items: stretch;
  position: fixed;
  left: 0;
  right: 0;
  top: 0;
  bottom: 0;
}
.gesture,
.error {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 2em;
}
video {
  transform: scale(-1, 1);
  object-fit: cover;
  object-position: center;
}
</style>

<template>
  <div class="loading container" v-show="state === 'loading'">загрузка</div>
  <div class="gesture container" v-show="state === 'gesturing'" ref="gesture">
    Нажмите чтобы начать
  </div>
  <div class="error container" v-show="state === 'error'">
    {{ error }}
  </div>
  <div class="grid container" v-show="state === 'loaded'">
    <video :srcObject.prop="stream" autoplay muted />
    <video v-for="p in peers" :key="p.id" :srcObject.prop="p.stream" autoplay />
  </div>
</template>

<script>
import { reactive } from "vue";

const ping = "ping";
const pong = "pong";
const initialized = "initialized";
const joined = "joined";
const left = "left";
const offer = "offer";
const answer = "answer";
const iceCandidate = "ice-candidate";

const loading = "loading";
const gesturing = "gesturing";
const error = "error";
const loaded = "loaded";

export default {
  name: "App",
  data() {
    return {
      stream: null,
      error: null,
      peers: [],
      ws: {
        connection: null,
        connected: false,
      },
      state: loading,
    };
  },
  methods: {
    newPeer(id, bePolite) {
      const p = reactive({
        id,
        bePolite,
        connection: new RTCPeerConnection(),
        stream: null,
      });

      p.connection.onicecandidate = (e) => {
        if (!e.candidate || !e.candidate.candidate) {
          return;
        }
        this.wsSend(iceCandidate, e.candidate, id);
      };

      p.connection.ontrack = (e) => {
        const {
          streams: [stream],
        } = e;
        p.stream = stream;
        stream.onremovetrack = () => {
          p.stream = null;
        };
      };

      this.peers.push(p);

      return p;
    },
    getPeer(id) {
      return this.peers.find((p) => p.id === id);
    },
    removePeer(id) {
      const i = this.peers.findIndex((p) => p.id === id);
      this.peers.splice(i, 1);
    },
    async handleJoined(id) {
      const p = this.newPeer(id, false);
      this.stream
        .getTracks()
        .forEach((t) => p.connection.addTrack(t, this.stream));
      const o = await p.connection.createOffer();
      await p.connection.setLocalDescription(o);
      this.wsSend(offer, o, id);
    },
    async handleOffer(id, offer) {
      let p = this.getPeer(id);
      let newPeer = false;
      if (!p) {
        p = this.newPeer(id, true);
        newPeer = true;
      }

      if (p.bePolite && p.connection.signalingState !== "stable") {
        await p.connection.setLocalDescription({ type: "rollback" });
      }
      await p.connection.setRemoteDescription(offer);

      if (newPeer) {
        this.stream
          .getTracks()
          .forEach((t) => p.connection.addTrack(t, this.stream));
      }

      const a = await p.connection.createAnswer();
      await p.connection.setLocalDescription(a);

      this.wsSend(answer, a, p.id);
    },
    async handleAnswer(id, answer) {
      await this.getPeer(id).connection.setRemoteDescription(answer);
    },
    async handleIceCandidate(id, iceCandidate) {
      await this.getPeer(id).connection.addIceCandidate(iceCandidate);
    },
    wsSend(type, data, to) {
      const msg = { type };
      if (data) {
        msg.data = data;
      }
      if (to) {
        msg.to = to;
      }
      this.ws.connection.send(JSON.stringify(msg));
    },
    wsConnect() {
      this.ws.connection = new WebSocket(this.wsURL);

      this.ws.connection.onopen = () => {
        this.ws.connected = true;
      };

      this.ws.connection.onmessage = (e) => {
        const msg = JSON.parse(e.data);

        if (msg.type === ping) {
          this.wsSend(pong, msg.data);
          return;
        }

        switch (msg.type) {
          case initialized:
            this.state = loaded;
            break;
          case joined:
            this.handleJoined(msg.from);
            break;
          case left:
            this.removePeer(msg.from);
            break;
          case offer:
            this.handleOffer(msg.from, msg.data);
            break;
          case answer:
            this.handleAnswer(msg.from, msg.data);
            break;
          case iceCandidate:
            this.handleIceCandidate(msg.from, msg.data);
            break;
        }
      };

      this.ws.connection.onclose = (e) => {
        this.ws.connected = false;
        this.state = loading;
        this.peers.splice(0, this.peers.length);
        if (e.code !== 1000) {
          this.wsConnect();
        }
      };
    },
    gesture() {
      this.state = gesturing;
      return new Promise((resolve) => {
        this.$refs.gesture.addEventListener("click", resolve, { once: true });
      });
    },
    async initStream() {
      this.stream = await navigator.mediaDevices.getUserMedia({
        audio: true,
        video: {
          facingMode: "user",
        },
      });
    },
  },
  async mounted() {
    await this.gesture();

    try {
      await this.initStream();
    } catch {
      this.state = error;
      this.error = "Не удалось инициализировать микрофон и камеру";
      return;
    }

    this.wsConnect();
  },
};
</script>
