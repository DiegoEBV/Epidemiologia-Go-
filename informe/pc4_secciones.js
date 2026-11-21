// Secciones del Entregable 2 (PC4): distribución, API, bases de datos y
// evidencias. Lo usa build_pc4.js; todas las cifras se leen de results/pc4.
const fs = require("fs");
const path = require("path");

module.exports = function seccionesPC4(h) {
  const { P, H1, H2, H3, B, N, IMG, TABLE, CODE, NOTE, SRC, fmt, pct, ROOT } = h;
  const J = (f) => JSON.parse(fs.readFileSync(path.join(ROOT, "results/pc4", f), "utf8"));
  const TXT = (f) => fs.readFileSync(path.join(ROOT, "results/pc4", f), "utf8").trim();
  const CAPT = (f) => "results/pc4/capturas/" + f;

  const bench = J("bench_cluster.json").resultados;
  const lat = J("latencia.json");
  const latE = J("latencia_entrenando.json");
  const ruta = (L, r) => L.resumenes.find((x) => x.ruta === r);
  const tot = ruta(lat, "TOTAL"), pred = ruta(lat, "POST /api/predicciones");
  const totE = ruta(latE, "TOTAL"), predE = ruta(latE, "POST /api/predicciones");
  const newman = J("newman.json").run;
  const nmStats = newman.stats, nmT = newman.timings;
  const nmMax = Math.max(...newman.executions.map((e) => e.response.responseTime));
  const mod = J("api_modelo.json");
  const ds = J("api_dataset.json");
  const imp = J("api_impacto_top15.json");
  const met = J("api_metricas.json");
  const cl = J("api_cluster.json");
  const casos = J("casos_prueba.json");
  const b1 = bench[0], b4 = bench[bench.length - 1];
  const cols = met.colecciones_mongo;

  const s = [];
  const add = (...xs) => xs.flat().forEach((x) => s.push(x));

  // =====================================================================
  // 8. DISTRIBUCIÓN
  // =====================================================================
  add(H1("8. Distribución: clúster de nodos ML"),
    P(`En la PC3 los workers del Parameter Server eran goroutines dentro de un mismo proceso. En esta entrega cada worker es un **proceso independiente en su propio contenedor Docker** (\`node1\` … \`node4\`), y la API actúa como coordinador: reparte los datos por TCP, difunde los pesos, reúne los gradientes y combina los resultados. El algoritmo no cambió: se extrajo la lógica del worker a un tipo reutilizable (\`ml.ShardLocal\`) y la del coordinador a una función (\`ml.Coordinar\`) que recibe cualquier “calculador” de gradientes. Así, el mismo código sirve con goroutines (PC3) y con nodos remotos (PC4), y las pruebas comprueban que ambos producen exactamente el mismo modelo.`),
    H2("8.1. Arquitectura de despliegue"),
    P("La Figura 11 muestra los siete contenedores que levanta `docker compose up`: cuatro nodos ML, la API coordinadora, MongoDB y Redis. Se usan tres redes de Docker con propósitos distintos."),
    ...IMG("pc4_arq.png", 6.3, 3.36, "Arquitectura de despliegue de la PC4: contenedores, redes y protocolos."),
    ...TABLE(["Contenedor", "Imagen", "Redes", "Recursos", "Función"], [
      ["node1 … node4", "epidemiologia-go/node:pc4 (16 MB)", "cluster", "1 CPU, 512 MB, GOMAXPROCS=1", "Guardar su fragmento, calcular gradientes y puntuar casos"],
      ["api", "epidemiologia-go/api:pc4 (28 MB)", "publica, cluster, datos", "2 GB", "REST/WebSocket, coordinador del clúster, carga de CSV"],
      ["mongo", "mongo:7", "datos", "volumen mongo-data", "Persistencia (7 colecciones)"],
      ["redis", "redis:7-alpine", "datos", "256 MB, LRU, sin disco", "Caché, último latido de nodos, pub/sub"],
    ], [1500, 2100, 1500, 1800, 2126], "Servicios definidos en docker-compose.yml"),
    P("Las imágenes de Go se construyen con un `Dockerfile` multi-etapa: una etapa `golang:1.24-alpine` compila los binarios estáticos (`CGO_ENABLED=0`) y cada imagen final copia solo el suyo sobre `alpine:3.20` y lo ejecuta con un usuario sin privilegios. Los CSV del MINSA no se copian a ninguna imagen: se montan en la API como volumen de solo lectura (`./data:/data:ro`)."),
    H2("8.2. Justificación de la topología de red"),
    P("Se eligió una **topología en estrella**: cada nodo mantiene una única conexión TCP persistente con el coordinador y los nodos nunca se comunican entre sí. La tabla siguiente compara las alternativas que se evaluaron para este problema."),
    ...TABLE(["Topología", "Mensajes por paso", "Ventajas", "Desventajas para este caso", "Decisión"], [
      ["**Estrella (Parameter Server síncrono)**", "2P (P envíos de pesos + P gradientes)", "Una sola versión de los pesos; fácil de razonar, de monitorear y de proteger; tolera la caída de un nodo excluyéndolo", "El coordinador es punto único de falla y su enlace concentra el tráfico", "**Elegida**"],
      ["Anillo (Ring All-Reduce)", "2(P−1) por nodo", "Reparte el ancho de banda; sin servidor central", "Pensado para vectores enormes (redes neuronales); aquí el gradiente tiene 33 valores (264 bytes) y la latencia crece con P; si un nodo cae se rompe el anillo", "Descartada"],
      ["Malla completa (all-to-all)", "P(P−1)", "Sin coordinador", "Tráfico cuadrático, cada nodo necesita conocer a todos y no hay un lugar natural para exponer la API", "Descartada"],
      ["Parameter Server asíncrono", "2 por nodo, sin barrera", "No espera al nodo más lento", "Gradientes calculados con pesos desactualizados; el resultado deja de ser determinista y reproducible", "Descartada"],
    ], [1700, 1500, 2100, 2526, 1200], "Comparación de topologías evaluadas"),
    P("Las razones de la elección son cuatro:"),
    N("**El mensaje es diminuto.** Cada paso envía 32 pesos + 1 sesgo (≈ 300 bytes con gob) y recibe un gradiente del mismo tamaño. Con mensajes así, lo que domina es la latencia del viaje de ida y vuelta (medida en " + fmt(cl.nodos[0].rtt_ms, 2) + " ms por nodo en la red de Docker), no el ancho de banda. La estrella tiene el mínimo de saltos posible: uno de ida y uno de vuelta en paralelo con todos los nodos.", "num5"),
    N("**Determinismo.** Con una barrera síncrona y la suma de gradientes hecha en orden de worker, el modelo distribuido es idéntico al de la PC3 (prueba `TestDistribuidoIgualLocal`). Esto permite comparar tiempos con 1, 2, 3 y 4 nodos sabiendo que el resultado no cambia.", "num5"),
    N("**El coordinador ya existe.** El enunciado pide que la API sea el coordinador del clúster. En estrella, la API es naturalmente el centro: recibe las solicitudes, reparte el trabajo y guarda los resultados en las bases de datos.", "num5"),
    N("**Aislamiento y seguridad.** Las redes `cluster` y `datos` se declaran `internal: true`: no tienen salida a Internet ni puertos publicados. Los nodos solo ven a la API (no pueden llegar a MongoDB ni a Redis) y las bases solo ven a la API. El único puerto expuesto al exterior es el 8080 de la API. Cada nodo se limita a 1 CPU y 512 MB para que se comporte como una máquina independiente y el benchmark mida escalamiento real y no el reparto de núcleos de un mismo host.", "num5"),
    P("La desventaja principal de la estrella, que el coordinador es punto único de falla, se mitiga con el estado persistido: si la API se reinicia, recupera el modelo activo desde MongoDB (no reentrena si los CSV no cambiaron) y marca como fallidos los entrenamientos que quedaron a medias."),
    H2("8.3. Protocolo de comunicación coordinador–nodo"),
    P("Sobre cada conexión TCP viajan mensajes codificados con `encoding/gob` (serialización binaria de la biblioteca estándar) en modo petición-respuesta: el coordinador envía un `Sobre` y el nodo responde con otro. Como cada conexión tiene a lo sumo una petición en vuelo, no hacen falta identificadores de correlación. La Figura 12 muestra la secuencia completa de un entrenamiento."),
    ...TABLE(["Mensaje (coordinador → nodo)", "Respuesta (nodo → coordinador)", "Cuándo", "Contenido"], [
      ["SHARD", "SHARD_OK", "Al inicio de cada entrenamiento", "Fragmento de entrenamiento en formato columnar (edad, sexo, departamento, ola, etiqueta: 5 bytes por registro), id del worker, mini-lote local y normalización"],
      ["PESOS", "GRADIENTE", "En cada paso (234 por época con 4 nodos)", "w[32], b, época y paso → ∇w[32], ∇b, pérdida, peso total y muestras"],
      ["IMPACTO", "IMPACTO_OK", "Al terminar el entrenamiento", "Modelo y casos recientes con su provincia → sumas parciales por provincia (map-reduce)"],
      ["PING", "PONG", "Cada 2 s (latido)", "Métricas del nodo: % de CPU, heap, goroutines, registros, pasos y uptime"],
    ], [1900, 1900, 1800, 3426], "Mensajes del protocolo TCP"),
    ...IMG("pc4_seq.png", 6.0, 3.93, "Secuencia de mensajes de un entrenamiento distribuido."),
    P(`El formato columnar es una decisión de rendimiento: gob codifica cada columna \`[]uint8\` como un único bloque de bytes, de modo que un fragmento de ${fmt(b4.detalle_nodos[0].registros)} registros pesa unos ${fmt((b4.detalle_nodos[0].registros * 5) / 1e6, 1)} MB y se envía en ${fmt(b4.detalle_nodos[0].envio_ms, 0)} ms. Enviar los ${fmt(ds.n_train)} registros de entrenamiento a los 4 nodos en paralelo tomó ${fmt(b4.distribucion_s, 2)} s.`),
    H2("8.4. Funcionamiento en lenguaje Go"),
    H3("Nodo: una goroutine por conexión"),
    P("El nodo (`cmd/node`, `internal/cluster/node.go`) acepta conexiones en el puerto 9000 y atiende cada una en su propia goroutine. El bucle decodifica un `Sobre`, lo procesa y codifica la respuesta. El cálculo del gradiente es exactamente el de la PC3 (`ShardLocal.Responder`)."),
    ...CODE(SRC("internal/cluster/node.go", /^func \(n \*Nodo\) atender/, /^}/), "Código 5. Bucle de atención del nodo (internal/cluster/node.go)"),
    H3("Coordinador: proxy por nodo con exclusión mutua"),
    P("Dentro de la API cada nodo se representa con un `NodoRemoto`, que mantiene la conexión TCP, su codificador y su decodificador gob. Un `sync.Mutex` garantiza que solo haya una petición en vuelo por conexión, así un latido nunca se mezcla con un gradiente. Si la conexión falla, se descarta y la siguiente llamada vuelve a conectar (reconexión perezosa). `NodoRemoto` implementa la interfaz `ml.Calculador`, de modo que el coordinador lo trata igual que a un worker local."),
    ...CODE(SRC("internal/cluster/client.go", /^func \(n \*NodoRemoto\) Llamar/, /^}/), "Código 6. Petición-respuesta sobre TCP con deadline (internal/cluster/client.go)"),
    H3("Distribución de los fragmentos en paralelo"),
    P("`Cluster.Entrenar` reparte el set de entrenamiento con la misma semilla que la PC3 y envía cada fragmento desde su propia goroutine; un `sync.WaitGroup` espera a que todos los nodos confirmen y `errors.Join` reúne los errores. Cada goroutine escribe solo en su posición del slice `info.Nodos[w]`, por lo que no hay escrituras compartidas."),
    ...CODE(SRC("internal/cluster/coordinator.go", /^\tt0 := time.Now\(\)/, /^\tinfo.DistribucionS/), "Código 7. Envío concurrente de fragmentos (internal/cluster/coordinator.go)"),
    H3("Parameter Server: difusión, barrera y reducción"),
    P("En cada paso `ml.Coordinar` lanza una goroutine por nodo que envía los pesos y espera su gradiente; el `WaitGroup` es la barrera síncrona. Después suma los gradientes **en orden de worker** (no en orden de llegada), lo que hace el resultado determinista, y actualiza los pesos. Esta función es la única que escribe `m.W` y `m.B`."),
    ...CODE(SRC("internal/ml/distrib.go", /\/\/ Broadcast: cada nodo recibe/, /^\t\t\tres.Pasos\+\+/), "Código 8. Paso del Parameter Server distribuido (internal/ml/distrib.go)"),
    H3("Impacto social como map-reduce en el clúster"),
    P("Al terminar el entrenamiento, el coordinador reparte los casos positivos recientes entre los nodos. Cada nodo los puntúa con el modelo y suma la letalidad esperada por provincia, a su vez en paralelo con `runtime.NumCPU()` goroutines con acumuladores propios (map local). El coordinador suma las respuestas (reduce) y obtiene el ranking. La prueba `TestImpactoDistribuidoIgualLocal` verifica que el resultado coincide con el cálculo local de la PC3."),
    H3("Latidos y tolerancia a fallos"),
    P("Una goroutine del coordinador hace `PING` a todos los nodos cada 2 s (en paralelo) y mide el tiempo de ida y vuelta. El último estado de cada nodo se guarda en Redis con TTL de 15 s (si la clave expira, el nodo está caído), una muestra va a MongoDB cada 10 s y el estado se difunde por WebSocket. Un entrenamiento solo usa los nodos cuyo último intercambio fue exitoso (`Cluster.Activos`). La prueba `TestNodoCaido` levanta un nodo vivo y uno caído y verifica que el clúster detecta la caída y entrena con el que queda."),
    H2("8.5. Evidencias de la distribución"),
    ...IMG(CAPT("01_compose_ps.png"), 6.3, 1.41, "Contenedores en ejecución (docker compose ps)."),
    ...IMG(CAPT("02_redes.png"), 6.3, 0.83, "Redes internas de Docker y contenedores conectados a cada una."),
    ...IMG(CAPT("03_arranque_api.png"), 6.3, 1.8, "Arranque del coordinador: conexión a los 4 nodos, carga de 4.8 M registros, entrenamiento distribuido e impacto."),
    ...IMG(CAPT("04_node1.png"), 6.3, 1.6, "Registro del nodo node1: recepción de fragmentos y tareas de impacto."),
    P(`**Escalamiento.** Con el script \`scripts/bench_cluster.py\` se lanzó, vía la API, el mismo entrenamiento (6 épocas, mini-lote global de 16 384) con 1, 2, 3 y 4 nodos. Cada nodo tiene 1 CPU, de modo que el speedup mide cuánto se gana al agregar máquinas.`),
    ...TABLE(["Nodos", "Entrenamiento (s)", "Envío de fragmentos (s)", "Pasos por época", "Speedup", "Eficiencia", "AUC test"], bench.map((r) =>
      [String(r.nodos), fmt(r.entrenamiento_s, 2), fmt(r.distribucion_s, 2), fmt(r.pasos_epoca), fmt(r.speedup, 2) + "×", fmt(100 * r.eficiencia, 0) + "%", fmt(r.auc, 4)]),
      [900, 1500, 1600, 1300, 1200, 1200, 1326], "Entrenamiento distribuido con 1 a 4 nodos (1 CPU por nodo)"),
    ...IMG("pc4_bench_cluster.png", 6.3, 2.36, "Tiempo, speedup y eficiencia del entrenamiento distribuido."),
    ...IMG(CAPT("06_bench_cluster.png"), 5.0, 2.07, "Salida del benchmark del clúster."),
    P(`Con 4 nodos el entrenamiento baja de ${fmt(b1.entrenamiento_s, 2)} s a ${fmt(b4.entrenamiento_s, 2)} s (speedup ${fmt(b4.speedup, 2)}×, eficiencia ${fmt(100 * b4.eficiencia, 0)}%), y el AUC es idéntico en los cuatro casos porque el mini-lote global se mantiene constante. La eficiencia cae poco a poco porque el tiempo de cómputo por paso se reparte entre más nodos, pero el viaje de ida y vuelta por TCP (≈ 0.3 ms) y la espera al nodo más lento en la barrera no se reducen. Por eso es clave que cada paso tenga suficiente trabajo (4 096 registros por nodo con 4 nodos). Comparado con la PC3, donde 2 goroutines en una máquina de 2 núcleos lograban 1.91×, aquí 2 nodos logran ${fmt(bench[1].speedup, 2)}×: separar los workers en procesos con su propia CPU elimina la competencia por los núcleos.`));

  // =====================================================================
  // 9. API
  // =====================================================================
  add(H1("9. Desarrollo de la API"),
    P("La API (`cmd/api`, paquete `internal/api`) está escrita con `net/http` de la biblioteca estándar, usando los patrones con método y comodines del enrutador de Go 1.22+ (`\"GET /api/entrenamientos/{id}\"`). Además de exponer las predicciones, es el coordinador del clúster de la sección 8. Solo usa tres dependencias externas: los controladores oficiales de MongoDB y Redis y `gorilla/websocket`. El JWT (HS256) y el hash de contraseñas (PBKDF2-SHA256) se implementaron con la biblioteca estándar."),
    H2("9.1. Endpoints"),
    ...TABLE(["Método y ruta", "Rol", "Descripción"], [
      ["GET /health", "público", "Estado del coordinador, nodos activos, MongoDB y Redis"],
      ["POST /api/auth/login", "público", "Valida usuario y contraseña (PBKDF2) y emite un JWT por 8 horas"],
      ["POST /api/predicciones", "JWT", "Riesgo de un caso: probabilidad, letalidad estimada, nivel, aporte de cada variable y recomendación"],
      ["POST /api/predicciones/lote", "JWT", "Hasta 1 000 casos en una llamada, con conteo por nivel"],
      ["GET /api/predicciones", "JWT", "Historial de predicciones guardado en MongoDB (filtro por departamento)"],
      ["GET /api/impacto", "JWT", "Ranking de provincias por casos graves esperados (Redis → MongoDB)"],
      ["GET /api/impacto/departamentos", "JWT", "Agregado por departamento (pipeline de agregación de MongoDB)"],
      ["GET /api/modelo · /api/modelos", "JWT", "Modelo activo (pesos, métricas, historial, distribución) y versiones"],
      ["GET /api/dataset", "JWT", "Resumen de la carga y limpieza de los CSV"],
      ["POST /api/entrenamientos", "admin", "Lanza un entrenamiento distribuido (202 Accepted); parámetros: nodos, épocas, lote, tasa"],
      ["GET /api/entrenamientos[/{id}]", "JWT", "Estado y progreso de los entrenamientos"],
      ["GET /api/cluster · /api/cluster/metricas", "JWT", "Estado de los nodos (latido, RTT, CPU, memoria) y su serie temporal"],
      ["GET /api/metricas", "JWT", "Latencia p50/p95/p99 por ruta, tasa de caché, conteo de colecciones"],
      ["GET /ws?token=", "JWT", "WebSocket: eventos de estado, latidos del clúster y progreso del entrenamiento"],
      ["GET /swagger/", "público", "Documentación interactiva (OpenAPI 3.0)"],
    ], [3100, 1000, 4926], "Endpoints de la API"),
    H2("9.2. Documentación con Swagger"),
    P("La especificación OpenAPI 3.0 (`internal/api/swagger/openapi.yaml`) describe cada endpoint, sus esquemas de entrada y salida, ejemplos, códigos de error y el esquema de seguridad Bearer JWT. El archivo y la página de Swagger UI se incrustan en el binario con `//go:embed`, de modo que la documentación viaja con el ejecutable y se sirve en `http://localhost:8080/swagger/`."),
    ...IMG(CAPT("18_swagger.png"), 5.2, 6.76, "Swagger UI de la API."),
    ...IMG(CAPT("19_swagger_prediccion.png"), 5.4, 5.4, "Detalle de POST /api/predicciones en Swagger: ejemplos de entrada y esquema de respuesta."),
    H2("9.3. Funcionamiento en lenguaje Go"),
    H3("Enrutador y cadena de middleware"),
    P("Las rutas se registran en un `http.ServeMux`. Las protegidas se envuelven con `conAuth`, que valida el JWT y, si se indica, el rol. Toda petición pasa por una cadena de middleware: recuperación de pánicos → CORS → medición de latencia. El medidor envuelve el `ResponseWriter` para escribir la cabecera `X-Response-Time` justo antes de enviar los encabezados y registrar la duración por ruta en un buffer circular, del que `/api/metricas` calcula los percentiles."),
    ...CODE(SRC("internal/api/server.go", /^func NuevoServidor/, /^}/), "Código 9. Registro de rutas (internal/api/server.go)"),
    H3("Camino crítico de una predicción"),
    P("El requisito de responder en menos de 100 ms guió el diseño del endpoint de predicción:"),
    B("**Sin locks en la lectura del modelo.** El modelo activo se guarda en un `atomic.Pointer`. Un entrenamiento nuevo publica el modelo con un intercambio atómico (`Store`), y las predicciones en curso siguen usando la versión anterior sin esperar."),
    B("**Cálculo mínimo.** Gracias a la codificación dispersa, aplicar el modelo cuesta 5 multiplicaciones y una exponencial: microsegundos."),
    B("**Caché en Redis.** La respuesta se guarda con la clave `pred:v{versión}:{edad}:{sexo}:{dept}:{ola}` y TTL de 1 hora; las consultas repetidas se responden desde Redis. Cada operación con Redis tiene un tiempo límite de 20 ms."),
    B("**Auditoría asíncrona.** La predicción se registra en MongoDB, pero la petición no espera a la base: se encola en un canal con buffer de 16 384 posiciones y una goroutine escritora la inserta en lotes (`InsertMany` de hasta 500 documentos o cada 500 ms). Si el canal se llenara, se descarta la auditoría y se cuenta, priorizando la latencia."),
    ...CODE(SRC("internal/api/app.go", /^func \(a \*App\) Predecir/, /^}/), "Código 10. Predicción con caché y auditoría asíncrona (internal/api/app.go)"),
    H3("Entrenamiento como trabajo en segundo plano"),
    P("`POST /api/entrenamientos` responde 202 de inmediato y ejecuta el trabajo en una goroutine. Un `atomic.Bool` con `CompareAndSwap` garantiza que solo haya un entrenamiento a la vez (si no, 409 Conflict). Al final de cada época el trabajo se actualiza en MongoDB y se publica un evento en el canal `eventos` de Redis; cada réplica de la API está suscrita y reenvía el evento a sus clientes WebSocket a través de un hub, cuya única goroutine es dueña del mapa de clientes (altas, bajas y difusión por canales, sin mutex)."),
    H3("Seguridad"),
    P("Las contraseñas se guardan con PBKDF2-HMAC-SHA256 (60 000 iteraciones, sal aleatoria de 16 bytes) y se comparan en tiempo constante. El JWT se firma con HMAC-SHA256 y se verifica en tiempo constante (`hmac.Equal`), con control de expiración. El rol `admin` es el único que puede entrenar; el rol `analista` recibe 403. Los cuerpos JSON se limitan a 1 MB y se rechazan campos desconocidos. El secreto del JWT y las contraseñas iniciales se configuran con variables de entorno."),
    H2("9.4. Pruebas con Postman y latencia menor a 100 ms"),
    P(`Se construyó la colección \`postman/TF-CC65-Caso1-PC4.postman_collection.json\` con ${nmStats.requests.total} peticiones en cuatro carpetas (sistema y autenticación, predicciones, impacto, modelo/clúster/entrenamiento). Cada petición tiene pruebas (scripts \`pm.test\`) que verifican el código de estado, el contenido de la respuesta y que el tiempo de respuesta (\`pm.response.responseTime\`) sea **menor a 100 ms**. El token del login se guarda en una variable de la colección y se reutiliza en las demás peticiones. La colección se ejecutó con Newman, el ejecutor de línea de comandos oficial de Postman.`),
    NOTE(`**Resultado:** ${nmStats.requests.total} peticiones, ${nmStats.assertions.total} aserciones, **${nmStats.assertions.failed} fallidas**. Tiempo de respuesta promedio ${fmt(nmT.responseAverage, 0)} ms, máximo ${fmt(nmMax, 0)} ms (login, por el costo deliberado de PBKDF2). Todas las respuestas, incluidas las de error, llegaron en menos de 100 ms.`),
    NOTE("==Agregar aquí capturas de la colección ejecutada en la aplicación de Postman (Runner) si el docente las requiere de la interfaz gráfica: importar postman/TF-CC65-Caso1-PC4.postman_collection.json y ejecutar Run collection.=="),
    ...IMG(CAPT("20_newman_html.png"), 5.4, 5.78, "Reporte HTML de la ejecución de la colección de Postman con Newman."),
    ...IMG(CAPT("09_newman_1.png"), 6.0, 4.18, "Ejecución de la colección (1/3): sistema y autenticación."),
    ...IMG(CAPT("10_newman_2.png"), 6.3, 6.4, "Ejecución de la colección (2/3): predicciones e impacto."),
    ...IMG(CAPT("11_newman_3.png"), 6.3, 6.15, "Ejecución de la colección (3/3): modelo, clúster, entrenamiento y resumen."),
    H3("Prueba de carga"),
    P(`Una petición aislada no demuestra que el sistema responda rápido bajo uso real. Por eso se escribió una herramienta de carga en Go (\`cmd/loadtest\`): ${lat.concurrencia} goroutines clientes envían peticiones sin pausa durante ${fmt(lat.duracion_s, 0)} s, con una mezcla de 80% predicciones aleatorias, 10% ranking de impacto y 10% estado del clúster, y se registran los percentiles de cada ruta.`),
    ...TABLE(["Ruta", "Peticiones", "Errores", "Req/s", "p50 (ms)", "p95 (ms)", "p99 (ms)", "Máx. (ms)", "< 100 ms"], lat.resumenes.map((r) =>
      [r.ruta, fmt(r.peticiones), String(r.errores), fmt(r.rps), fmt(r.p50_ms, 2), fmt(r.p95_ms, 2), fmt(r.p99_ms, 2), fmt(r.max_ms, 2), fmt(r.pct_bajo_100ms, 1) + "%"]),
      [2300, 1050, 750, 850, 800, 800, 800, 850, 826], `Prueba de carga: ${lat.concurrencia} clientes concurrentes durante ${fmt(lat.duracion_s, 0)} s`),
    ...IMG(CAPT("07_loadtest.png"), 6.3, 1.51, "Salida de la prueba de carga."),
    ...IMG("pc4_latencia_hist.png", 6.3, 2.48, "Distribución de la latencia de las predicciones bajo carga."),
    P(`La API atendió ${fmt(tot.peticiones)} peticiones (${fmt(tot.rps)} por segundo) sin errores. La mediana de las predicciones fue de ${fmt(pred.p50_ms, 2)} ms, el percentil 99 de ${fmt(pred.p99_ms, 2)} ms y la peor respuesta de ${fmt(pred.max_ms, 1)} ms: el **100% de las peticiones respondió en menos de 100 ms**, con un margen de más de 10 veces en el p99.`),
    P(`También se repitió la prueba mientras el clúster ejecutaba un entrenamiento de 20 épocas, para comprobar que el entrenamiento no degrada el servicio. La latencia sube algo (p99 de ${fmt(predE.p99_ms, 2)} ms, máximo ${fmt(predE.max_ms, 1)} ms) porque la API también coordina los pasos del Parameter Server y el host comparte CPU, pero el 100% de las ${fmt(totE.peticiones)} peticiones siguió por debajo de 100 ms. Al terminar, el modelo nuevo se activó sin cortar el servicio.`),
    ...IMG(CAPT("08_loadtest_entrenando.png"), 6.3, 1.51, "Prueba de carga durante un entrenamiento distribuido."),
    ...IMG("pc4_latencia_rutas.png", 6.3, 2.48, "Percentiles de latencia por endpoint, en reposo y durante un entrenamiento."));

  // =====================================================================
  // 10. BASES DE DATOS
  // =====================================================================
  add(H1("10. Implementación de bases de datos"),
    P("Siguiendo el enunciado, la solución tiene dos niveles de almacenamiento con responsabilidades separadas:"),
    B("**MongoDB (persistente):** guarda todo lo que debe sobrevivir a un reinicio y lo que se consulta como historial: usuarios, versiones del modelo, entrenamientos, auditoría de predicciones, ranking de impacto, resumen del dataset y métricas del clúster. Se eligió un modelo de documentos porque las entidades tienen estructuras anidadas y variables (historial por época, detalle de distribución por nodo, matriz de confusión) que en un modelo relacional exigirían varias tablas y uniones."),
    B("**Redis (caché / colaborativo):** guarda lo que se lee muchas veces y puede regenerarse: predicciones ya calculadas, el ranking de impacto precalculado, el modelo activo para que otras réplicas lo carguen, el último latido de cada nodo con expiración y un canal pub/sub para difundir eventos a todas las réplicas de la API. Se configuró con 256 MB, política de expulsión LRU y sin persistencia en disco, porque todo su contenido se puede reconstruir desde MongoDB o recalcular."),
    H2("10.1. Diseño de entidades"),
    ...IMG("pc4_er.png", 6.3, 3.67, "Modelo de documentos de MongoDB y claves de Redis."),
    ...TABLE(["Colección", "Clave (_id)", "Campos principales", "Relaciones / índices", "Escrita por"], [
      ["usuarios", "ObjectId", "username, pass_hash, salt, iter, rol, creado", "username único", "Siembra al arrancar"],
      ["modelos", "int (versión)", "w[32], b, normalizacion, pesos{}, metricas{}, historial[], distribucion{}, config{}, n_train, n_test, huella_datos, activo", "trabajo_id → entrenamientos; índice activo", "Fin de cada entrenamiento"],
      ["entrenamientos", "string (fecha-aleatorio)", "estado, solicitante, config{}, nodos_solicitados, inicio, fin, historial[], distribucion{}, metricas{}, error", "solicitante → usuarios; modelo_version → modelos; índices creado, estado", "Cada época y cambio de estado"],
      ["predicciones", "ObjectId", "ts, usuario, entrada{edad, sexo, departamento, provincia, fecha}, probabilidad, letalidad, nivel, modelo_version, latencia_us, cache", "usuario → usuarios; modelo_version → modelos; índices ts, (departamento, ts)", "Escritor asíncrono en lotes"],
      ["impacto_provincias", "\"v{n}|DEPT|PROV\"", "modelo_version, ranking, departamento, provincia, positivos, graves_esperados, tasa_por_1000, pct_mayores_60, mes_desde", "modelo_version → modelos; índices (versión, ranking), (versión, departamento)", "Map-reduce del clúster"],
      ["dataset_resumen", "\"actual\"", "limpieza[], positivos, fallecidos, letalidad_global, letalidad_por_edad{}, n_train, n_test, huella, carga_s", "documento único", "Carga de los CSV"],
      ["metricas_cluster", "ObjectId", "ts, nodo, conectado, rtt_ms, cpu_pct, heap_mb, registros, pasos", "índice TTL de 24 h sobre ts; (nodo, ts)", "Latidos (cada 10 s)"],
    ], [1500, 1300, 3100, 1926, 1200], "Entidades (colecciones) de MongoDB"),
    P("Las relaciones son por referencia (versión del modelo, id del trabajo, usuario) y no por incrustación, porque cada entidad tiene su propio ciclo de vida: un modelo se consulta sin su trabajo, y miles de predicciones apuntan a la misma versión. Lo que sí se incrusta es lo que pertenece a un único documento y siempre se lee junto con él: la entrada de la predicción, el historial por época y el detalle de distribución por nodo. La versión del modelo forma parte del `_id` del impacto y de la clave de caché de Redis: al activar una versión nueva se invalida la caché anterior sin riesgo de mezclar resultados."),
    ...TABLE(["Clave de Redis", "Tipo / TTL", "Contenido", "Uso"], [
      ["modelo:activo", "string JSON · sin TTL", "Documento del modelo activo", "Arranque rápido de réplicas de la API"],
      ["pred:v{v}:{edad}:{sexo}:{dept}:{ola}", "string JSON · 1 h", "Respuesta completa de la predicción", "Caché del endpoint de predicción"],
      ["impacto:v{v}:{dept}:{n}", "string JSON · 10 min", "Ranking top-n (opcionalmente por departamento)", "Caché de GET /api/impacto"],
      ["cluster:nodo:{id}", "string JSON · 15 s", "Último latido del nodo", "Si expira, el nodo se considera caído"],
      ["stats:predicciones", "contador (INCRBY)", "Total de predicciones registradas", "Métrica global compartida por réplicas"],
      ["eventos", "canal pub/sub", "Eventos de entrenamiento, clúster y estado", "Difusión a los WebSocket de todas las réplicas"],
    ], [2700, 1700, 2500, 2126], "Claves de Redis"),
    H2("10.2. Implementación en Go"),
    P("El paquete `internal/store` encapsula ambos niveles. `ConectarMongo` y `ConectarRedis` reintentan hasta que el servidor responde (en Docker Compose las bases pueden tardar en estar listas). `CrearIndices` declara los índices de forma idempotente en cada arranque. Las operaciones se escriben como métodos con tiempo límite (`context.WithTimeout`): reemplazo con upsert para modelos y entrenamientos, `InsertMany` no ordenado para predicciones e impacto, búsquedas con orden y proyección, y un pipeline de agregación para el impacto por departamento."),
    ...CODE(SRC("internal/store/mongo.go", /^func \(m \*Mongo\) ImpactoPorDepartamento/, /^}/), "Código 11. Agregación de impacto por departamento (internal/store/mongo.go)"),
    H2("10.3. Registros almacenados"),
    P(`Al terminar las pruebas de esta entrega la base contenía ${fmt(cols.predicciones)} predicciones auditadas, ${cols.modelos} versiones del modelo con sus ${cols.entrenamientos} entrenamientos, ${fmt(cols.impacto_provincias)} filas de impacto (${fmt(cols.impacto_provincias / cols.modelos, 0)} provincias por versión) y ${cols.metricas_cluster} muestras de métricas de los nodos. La tasa de aciertos de la caché de predicciones fue de ${pct(met.tasa_cache)}.`),
    ...TABLE(["Colección", "Documentos"], Object.entries(cols).map(([k, v]) => [k, fmt(v)]), [5000, 4026], "Documentos por colección al final de las pruebas"),
    ...IMG(CAPT("12_mongo_1.png"), 6.3, 5.94, "MongoDB: conteo de colecciones, usuarios y modelo activo."),
    ...IMG(CAPT("13_mongo_2.png"), 6.3, 5.5, "MongoDB: documento de un entrenamiento distribuido."),
    ...IMG(CAPT("14_mongo_3.png"), 6.3, 5.9, "MongoDB: predicciones auditadas y ranking de impacto."),
    ...IMG(CAPT("15_mongo_4.png"), 6.3, 4.4, "MongoDB: métricas del clúster y resumen del dataset."),
    ...IMG(CAPT("16_mongo_indices.png"), 6.0, 4.18, "MongoDB: índices, plan de ejecución y consultas de agregación."),
    P("El plan de ejecución confirma que la consulta del ranking usa el índice compuesto `(modelo_version, ranking)`: examina solo los 15 documentos que devuelve, en 0 ms."),
    ...IMG(CAPT("17_redis.png"), 6.0, 3.54, "Redis: claves de caché, latidos de nodos con TTL, contador global y estadísticas de aciertos."));

  // =====================================================================
  // 11. EVIDENCIAS PC4
  // =====================================================================
  const pruebas = TXT("tests_race.log").split("\n").filter((l) => l.startsWith("--- PASS")).length;
  add(H1("11. Evidencias de la implementación y pruebas de funcionamiento"),
    H2("11.1. Estructura del código de la PC4"),
    ...TABLE(["Ruta", "Contenido"], [
      ["internal/ml/distrib.go", "ShardLocal (worker reutilizable), interfaz Calculador, Coordinar (Parameter Server), PartirShards, Explicar"],
      ["internal/cluster/protocol.go", "Sobre, tipos de mensaje y formato columnar de los fragmentos"],
      ["internal/cluster/node.go", "Servidor TCP del nodo, gradiente, impacto map-reduce, métricas de CPU"],
      ["internal/cluster/client.go", "NodoRemoto: conexión persistente, petición-respuesta, reconexión"],
      ["internal/cluster/coordinator.go", "Cluster: latidos, nodos activos, Entrenar e Impacto distribuidos"],
      ["internal/store/*.go", "Entidades, repositorio MongoDB (índices, agregaciones) y caché Redis"],
      ["internal/api/*.go", "Servidor HTTP, handlers, JWT/PBKDF2, métricas de latencia, WebSocket, arranque y trabajos"],
      ["internal/api/swagger/", "openapi.yaml y Swagger UI incrustados con go:embed"],
      ["cmd/node, cmd/api", "Ejecutables del nodo y de la API coordinadora"],
      ["cmd/loadtest", "Prueba de carga concurrente con percentiles"],
      ["Dockerfile, docker-compose.yml", "Imágenes multi-etapa y despliegue de los 7 contenedores"],
      ["postman/", "Colección de Postman con 22 peticiones y 87 aserciones"],
      ["scripts/bench_cluster.py, capturas.py, graficos_pc4.py", "Benchmark del clúster, capturas y gráficos del informe"],
    ], [3200, 5826], "Código agregado en la PC4"),
    H2("11.2. Pruebas automatizadas"),
    ...TABLE(["Prueba", "Paquete", "Qué verifica", "Resultado"], [
      ["TestDistribuidoIgualLocal", "cluster", "Con 1, 2 y 3 nodos TCP los pesos son idénticos (±1e-9) a los del entrenamiento con goroutines de la PC3", "PASS"],
      ["TestImpactoDistribuidoIgualLocal", "cluster", "El map-reduce de impacto en 3 nodos coincide con el cálculo local", "PASS"],
      ["TestNodoCaido", "cluster", "Un nodo caído se marca como desconectado y el entrenamiento usa solo los activos", "PASS"],
      ["TestJWT", "api", "Firma y verificación; rechaza secreto distinto, payload alterado, token expirado o mal formado", "PASS"],
      ["TestPassword", "api", "PBKDF2: acepta la correcta, rechaza la incorrecta, verifica hashes con el número anterior de iteraciones", "PASS"],
      ["TestValidacionEntrada", "api", "Edad, sexo, departamento (con tildes y minúsculas) y fecha → ola", "PASS"],
      ["TestNivelYCalculo", "api", "El riesgo crece con la edad, niveles BAJO/MODERADO/ALTO/MUY ALTO, 4 factores", "PASS"],
      ["TestPercentiles", "api", "Cálculo de p50, máximo, errores 5xx y % bajo 100 ms", "PASS"],
      ["TestRutasYAuth", "api", "401 sin token, 403 por rol, 200 con token, X-Response-Time, CORS y Swagger", "PASS"],
      ["7 pruebas de la PC3", "data, ml", "Siguen pasando tras extraer ShardLocal (Tabla 10)", "PASS"],
    ], [2800, 900, 4226, 1100], "Pruebas automatizadas de la PC4 (go test -race ./internal/...)"),
    ...IMG(CAPT("05_tests_race.png"), 4.6, 2.82, `Salida de go test -race: ${pruebas} pruebas en 4 paquetes, sin condiciones de carrera.`),
    H2("11.3. Datos de prueba y resultados"),
    P(`Se enviaron a la API casos representativos de distintos perfiles de riesgo y casos inválidos (cada uno dos veces, para observar la caché). Los resultados son coherentes con el EDA de la sección 4: el riesgo crece con la edad, es mayor en hombres y en las dos primeras olas, y cae fuertemente desde Ómicron.`),
    ...TABLE(["Caso", "Entrada", "HTTP", "Nivel / error", "Letalidad estimada", "X-Response-Time"], casos.map((c) => [
      c.caso, `${c.entrada.edad}, ${c.entrada.sexo}, ${c.entrada.departamento}${c.entrada.fecha ? ", " + c.entrada.fecha : ""}`, String(c.status),
      c.respuesta.nivel_riesgo || c.respuesta.error, c.respuesta.letalidad_estimada_pct || "—", c.x_response_time || "—"]),
      [2300, 2300, 700, 1700, 1100, 926], "Casos de prueba enviados a POST /api/predicciones (segunda llamada, servida desde Redis)"),
    P(`Por ejemplo, para un varón de 72 años en Lima durante la segunda ola el modelo estima una letalidad de ${casos[0].respuesta.letalidad_estimada_pct} (nivel MUY ALTO), mientras que para una mujer de 24 años en Cusco en el periodo post-olas estima ${casos[4].respuesta.letalidad_estimada_pct}. La respuesta incluye el aporte de cada variable al logit; en el primer caso la edad aporta ${fmt(casos[0].respuesta.factores[0].aporte_logit, 2)} y la segunda ola ${fmt(casos[0].respuesta.factores[3].aporte_logit, 2)}.`),
    H2("11.4. Modelo activo y respuesta a la pregunta de impacto"),
    ...TABLE(["Métrica", "Valor"], [
      ["Versión del modelo activo", `v${mod.version} (${mod.config.Epocas} épocas, ${mod.distribucion.nodos.length} nodos)`],
      ["AUC-ROC (test)", fmt(mod.metricas.AUC, 4)],
      ["Recall / precisión / exactitud", `${pct(mod.metricas.Recall)} / ${pct(mod.metricas.Precision)} / ${pct(mod.metricas.Accuracy)}`],
      ["Registros de entrenamiento / prueba", `${fmt(mod.n_train)} / ${fmt(mod.n_test)}`],
      ["Tiempo de entrenamiento distribuido", fmt(mod.duracion_s, 2) + " s"],
    ], [4500, 4526], "Modelo activo al cierre de las pruebas"),
    P(`El ranking de impacto se recalcula en el clúster cada vez que se entrena un modelo. Con los casos positivos registrados desde ${String(imp.casos_desde).slice(4)}/${String(imp.casos_desde).slice(0, 4)}, las 15 provincias con más casos graves esperados suman ${fmt(imp.total_graves_esperados, 0)}. Lima concentra la mayor cantidad por volumen de casos; Chiclayo, Arequipa y Trujillo le siguen. La tasa por 1 000 casos permite identificar provincias con menos casos pero pacientes de mayor edad, donde cada contagio pesa más.`),
    ...IMG("pc4_impacto_top.png", 6.3, 3.46, "Provincias con más casos graves esperados según el modelo activo (GET /api/impacto)."),
    ...TABLE(["#", "Provincia (departamento)", "Positivos", "Graves esperados", "Por 1 000", "% 60+"], imp.provincias.slice(0, 10).map((r) =>
      [String(r.ranking), `${r.provincia} (${r.departamento})`, fmt(r.positivos), fmt(r.graves_esperados, 1), fmt(r.tasa_por_1000, 1), fmt(r.pct_mayores_60, 1) + "%"]),
      [500, 3026, 1300, 1600, 1300, 1300], "Top 10 provincias por casos graves esperados (calculado en el clúster)"));

  return s;
};
