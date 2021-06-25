<!-- Стили web-страницы -->
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
.loading,
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

<!-- Шаблон web-страницы -->
<template>
  <div class="loading container" v-show="state === 'loading'">Загрузка</div>
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

<!-- Javascript web-страницы -->
<script>
import { reactive } from "vue";

// Типы сообщений, которые будут передаваться между сервером и клиентом или
// между клиентами.

// Ping и pong сообщения необходимы для поддержания websocket соединения.
const ping = "ping";
const pong = "pong";

// Cообщение о готовности websocket соединения.
const initialized = "initialized";

// Cообщение о подключении нового клиента.
const joined = "joined";

// Cообщение об отключении клиента.
const left = "left";

// Предложение о создании WebRTC-соединения.
const offer = "offer";

// Ответ на предложение о создании WebRTC-соединения.
const answer = "answer";

// Информация об ice-кандидате (потенциальном сетевом соединении).
const iceCandidate = "ice-candidate";

// Состояния web-страницы.

// Загрузка web-страницы.
const loading = "loading";

// Ожидание начального действия от пользователя
const gesturing = "gesturing";

// Ошибка web-страницы
const error = "error";

// Страницы загружена и работает.
const loaded = "loaded";

// Наше web-приложение.
export default {
  // Функция data возвращает начальное состояние web-приложения.
  data() {
    return {
      // Локальные видео и аудио потоки.
      stream: null,

      // Возможная ошибка при работе.
      error: null,

      // Хранилище пиров (WebRTC соединения).
      peers: [],

      // Websocket соединение с сервером.
      ws: {
        connection: null,
      },

      // Состояние web-приложения.
      state: loading,
    };
  },

  // Доступные методы.
  methods: {
    // Создание нового пира с указанным id и флагом "вежливости".
    newPeer(id, bePolite) {
      // Создание реактивной структуры пира.
      const p = reactive({
        // ID пира
        id,
        // Флаг "вежливости"
        bePolite,
        // WebRTC соединение.
        connection: new RTCPeerConnection(),
        // Аудио и видео поток пира.
        stream: null,
      });

      // Регистрация обработчика ice-кандидата. Здесь web-браузер передаёт нам
      // кандидаты на способы подключения с пиром.
      p.connection.onicecandidate = (e) => {
        // Если нет кандидата, а значит больше их не будет, то выходим из
        // обработчика.
        if (!e.candidate || !e.candidate.candidate) {
          return;
        }
        // Отправляем кандидат удалённому пиру.
        this.wsSend(iceCandidate, e.candidate, id);
      };

      // Регистрация обработчика появления нового трека (аудио или видео потока).
      // Здесь мы получаем аудио и видео поток от удалённого пира.
      p.connection.ontrack = (e) => {
        // Достаём поток.
        const {
          streams: [stream],
        } = e;
        // Сохраняем его структуру пира.
        p.stream = stream;
        // При удалении трека, удаляем его из структуры пира.
        stream.onremovetrack = () => {
          p.stream = null;
        };
      };

      // Добавляем пир в хранилище пиров.
      this.peers.push(p);

      // Возвращаем созданный трек.
      return p;
    },
    // Получения пира по id.
    getPeer(id) {
      return this.peers.find((p) => p.id === id);
    },
    // Удаление пира по id.
    removePeer(id) {
      const i = this.peers.findIndex((p) => p.id === id);
      this.peers[i].close();
      this.peers.splice(i, 1);
    },
    // Обработчик появления нового клиента (будущий пир).
    async handleJoined(id) {
      // Создаём новый пир, указываем его ID и устанавливаем "вежливость" в
      // ложь.
      const p = this.newPeer(id, false);
      // Получаем треки из аудио и видео потока и добавляем их в соединение
      // с пиром.
      this.stream
        .getTracks()
        .forEach((t) => p.connection.addTrack(t, this.stream));
      // Создаём предложение о соединении.
      const o = await p.connection.createOffer();
      // Сохраняем полученное предложение в соединении с пиром.
      await p.connection.setLocalDescription(o);
      // Отправляем предложение удалённому пиру.
      this.wsSend(offer, o, id);
    },
    // Обработчик предложения о соединении.
    async handleOffer(id, offer) {
      // Пытаемся получить пир, который соответствует отправителю.
      let p = this.getPeer(id);
      // Флаг того, что предложение от нового пира.
      let newPeer = false;
      // Если пир не найден, то значит надо его создать и, соответственно
      // установить флаг нового пира в истину.
      if (!p) {
        // Создаём новый пир, "вежливость" устанавливаем в истину.
        p = this.newPeer(id, true);
        newPeer = true;
      }

      // Если пир "вежливый", и состояние WebRTC-соединения не стабильное,
      // то откатываемся к последнему стабильному состоянию.
      if (p.bePolite && p.connection.signalingState !== "stable") {
        await p.connection.setLocalDescription({ type: "rollback" });
      }

      // Сохраняем полученное предложение о соединении.
      await p.connection.setRemoteDescription(offer);

      // Если это был новый пир, то нужно добавить в WebRTC-соединение треки
      // локальных аудио и видео потоков.
      if (newPeer) {
        this.stream
          .getTracks()
          .forEach((t) => p.connection.addTrack(t, this.stream));
      }

      // Создаём ответ на предложение о соединении.
      const a = await p.connection.createAnswer();

      // Сохраняем созданный ответ.
      await p.connection.setLocalDescription(a);

      // Отправляем ответ удалённому пиру.
      this.wsSend(answer, a, p.id);
    },
    // Обработчик ответа на предложение о соединении.
    async handleAnswer(id, answer) {
      // Сохраняем ответ в соответствущем пиру WebRTC-соединении.
      await this.getPeer(id).connection.setRemoteDescription(answer);
    },
    // Обработчик ice-кандидата
    async handleIceCandidate(id, iceCandidate) {
      // Добавляем полученный ice-кандидат в WebRTC-соединение, который
      // соответствует пиру-отправителю.
      await this.getPeer(id).connection.addIceCandidate(iceCandidate);
    },
    // Метод для отправки сообщений типа type, с данными data и адресатом to.
    wsSend(type, data, to) {
      // Формируем сообщение нужного типа.
      const msg = { type };
      // Добавляем данные, если они указаны.
      if (data) {
        msg.data = data;
      }
      // Добавляем получателя, если он указан.
      if (to) {
        msg.to = to;
      }
      // Отправляем сообщение на websocket-сервер для дальнейшей маршрутизации.
      this.ws.connection.send(JSON.stringify(msg));
    },
    // Метод подключения к websocket-серверу.
    wsConnect() {
      // Создаём соединение на указанный URL сервера.
      this.ws.connection = new WebSocket(this.wsURL);

      // Обработчик сообщений, которые приходят по websocket-соединению.
      this.ws.connection.onmessage = (e) => {
        // Декодируем сообщение.
        const msg = JSON.parse(e.data);

        // Если это ping сообщение.
        if (msg.type === ping) {
          // То отправляем pong полученными данными назад.
          this.wsSend(pong, msg.data);
          // Выходим из обработчика.
          return;
        }

        // Здесь смотрим тип сообщения и действуем согласно нему.
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

      // Обработчик закрытия websocket-соединения.
      this.ws.connection.onclose = (e) => {
        // Устанавливаем состояние в загрузку.
        this.state = loading;
        // Отключаемся от всех пиров.
        this.peers.forEach((p) => p.connection.close());
        // Удаляем все пиры.
        this.peers.splice(0, this.peers.length);
        // Если код закрытия соединения не "штатный".
        if (e.code !== 1000) {
          // То снова подключаемся.
          this.wsConnect();
        }
      };
    },
    // Метод для запуска ожидания начального действия от пользователя.
    gesture() {
      // Устанавливаем соответствующее состояние.
      this.state = gesturing;
      // Возвращаем ожидание на клик пользователем соответствующей области.
      return new Promise((resolve) => {
        this.$refs.gesture.addEventListener("click", resolve, { once: true });
      });
    },
    // Метод для инициализации аудио и видео потоков из микрофона и камеры
    // пользователя.
    async initStream() {
      this.stream = await navigator.mediaDevices.getUserMedia({
        audio: true,
        video: {
          facingMode: "user",
        },
      });
    },
  },
  // Обработчик старта нашего web-приложения.
  async mounted() {
    // Запускаем ожидания начального действия от пользователя.
    await this.gesture();

    // Пытаемся инциализировать аудио и видео потоки пользователя.
    try {
      await this.initStream();
    } catch {
      // Если не удалось, то меняем состояние web-приложения на ошибку.
      this.state = error;
      this.error = "Не удалось инициализировать микрофон и камеру";
      return;
    }

    // Подключаемся к websocket-серверу.
    this.wsConnect();
  },
};
</script>
