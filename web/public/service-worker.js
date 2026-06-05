const CACHE = "noobboard-v49";
const ASSETS = [
  "/",
  "/site-config.js",
  "/styles.css",
  "/app.js",
  "/manifest.json",
  "/app-icons/cloud-storage.svg",
  "/app-icons/container.svg",
  "/app-icons/database.svg",
  "/app-icons/dns-filter.svg",
  "/app-icons/download-client.svg",
  "/app-icons/media-automation.svg",
  "/app-icons/media-server.svg",
  "/app-icons/network.svg",
  "/app-icons/smart-home.svg",
  "/icons/noobboard-logo.svg",
  "/icons/apple-touch-icon.png",
  "/icons/icon-192.png",
  "/icons/icon-512.png",
  "/icons/maskable-512.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(ASSETS)).then(() => self.skipWaiting()));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(caches.keys()
    .then((keys) => Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key))))
    .then(() => self.clients.claim()));
});

self.addEventListener("fetch", (event) => {
  if (event.request.method !== "GET") return;
  event.respondWith(handleFetch(event.request));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const targetURL = new URL(event.notification?.data?.url || "/", self.location.origin).href;
  event.waitUntil(clients.matchAll({ type: "window", includeUncontrolled: true }).then((clientList) => {
    for (const client of clientList) {
      if (client.url.startsWith(self.location.origin) && "focus" in client) {
        return client.focus();
      }
    }
    if (clients.openWindow) return clients.openWindow(targetURL);
    return undefined;
  }));
});

async function handleFetch(request) {
  try {
    const response = await fetch(request);
    if (request.url.startsWith(self.location.origin) && ASSETS.includes(new URL(request.url).pathname)) {
      const cache = await caches.open(CACHE);
      await cache.put(request, response.clone());
    }
    return response;
  } catch (error) {
    const cached = await caches.match(request);
    if (cached) return cached;
    if (request.mode === "navigate") return caches.match("/");
    throw error;
  }
}
