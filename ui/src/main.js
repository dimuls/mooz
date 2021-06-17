import { createApp } from "vue";
import App from "./App.vue";

const wsPath = "/ws";
const wsScheme = location.protocol === "https:" ? "wss" : "ws";

let wsURL;

if (
  process.env.NODE_ENV === "production" ||
  process.env.NODE_ENV === "staging"
) {
  const hostname = location.hostname;
  const port = location.port ? `:${location.port}` : "";
  wsURL = `${wsScheme}://${hostname}${port}${wsPath}`;
} else {
  wsURL = `${wsScheme}://localhost:8080${wsPath}`;
}

const app = createApp(App);

app.config.globalProperties.wsURL = wsURL;

app.mount("#app");
