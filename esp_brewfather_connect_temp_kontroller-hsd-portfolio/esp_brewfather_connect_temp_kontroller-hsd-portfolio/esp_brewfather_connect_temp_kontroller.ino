
// https://randomnerdtutorials.com/esp32-ssd1306-oled-display-arduino-ide/

#include <WiFi.h>
#include <WebServer.h>
#include <ArduinoHttpClient.h>
#include <SPI.h>
#include <Wire.h> //kan kanskje fjernes
#include <Adafruit_GFX.h>
#include <Adafruit_SSD1306.h>
#include <Esp.h>

#define MAX6675_SCK  18
#define MAX6675_CS   19
#define MAX6675_SO   5

#define BLUE 0x001F

#define fridge_on 32
#define fridge_off 33
#define btn_s_down 26
#define btn_s_up 25


#define SCREEN_WIDTH 128 // OLED display width, in pixels
#define SCREEN_HEIGHT 64 // OLED display height, in pixels

Adafruit_SSD1306 display(SCREEN_WIDTH, SCREEN_HEIGHT, &Wire, -1);

// WiFi credentials are kept out of version control. Copy secrets.example.h to
// secrets.h and set your network there (secrets.h is gitignored).
#include "secrets.h"

const char* ssid = WIFI_SSID;
const char* password = WIFI_PASSWORD;

WiFiClient wifi;
WebServer server(80);

//pins
/*const int fridge_on = 32; // må endres ut ifra onenote
const int fridge_off = A3;
const int interrupt_id_increase = 0;
const int interrrupt_id_decrease = 1;*/

//variabler
int n = 5; //antall ganger temp skal summeres før avg. Prøver 100, siden temp ikke alltid blir registrert i brewfather 
int read_time = 1000; //tid i ms mellom hver temp måling for 15 min mellom post
String t_state = "";
int state;
float temperature_read;
float temperature_read_avg;
float sum;
int t = 0;
bool mode = false; //need to install a physical switch to change this bool
unsigned long lastRead = 0;
int readCount = 0;
volatile int prev_set_temperature = 20;

#define HISTORY_SIZE 96          // 24h at one reading per 15 min
#define HISTORY_INTERVAL 900000UL // 15 minutes in ms
float tempHistory[HISTORY_SIZE];
time_t timeHistory[HISTORY_SIZE];
int historyHead = 0;
int historyCount = 0;
unsigned long lastHistoryRead = 0;

// forward declarations
void handleHistory();
void handleRoot();
void handleData();
void renderDisplay();
void connect_to_wifi();
void thermometer_mode(bool mode);
void temp_compare(double temp);
void activate_sw(int pin);
void set_temp_decrease();
void set_temp_increase();
double readThermocouple();

//TEMPERATUR
volatile int set_temperature = 20; //må ha dette som volatile for å kunne bruke variablen i en interrupt ISR funksjon
double dT = 1; //tillat avvik fra set_temp
int temp_ctrl_activity;


void handleHistory() {
  String json = "[";
  for (int i = 0; i < historyCount; i++) {
    int idx = (historyHead + i) % HISTORY_SIZE;
    struct tm* ti = localtime(&timeHistory[idx]);
    char timeStr[6];
    strftime(timeStr, sizeof(timeStr), "%H:%M", ti);
    if (i > 0) json += ",";
    json += "{\"t\":\"";
    json += timeStr;
    json += "\",\"v\":";
    if (isnan(tempHistory[idx])) json += "null";
    else json += String(tempHistory[idx], 1);
    json += "}";
  }
  json += "]";
  server.send(200, "application/json", json);
}

void handleRoot() {
  String html = R"rawliteral(
<!DOCTYPE html><html>
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>Brew Temp</title>
  <style>
    body{font-family:sans-serif;background:#111;color:#eee;text-align:center;padding:20px;margin:0}
    h2{color:#e94560;margin-bottom:24px}
    .card{background:#1a1a2e;border-radius:12px;padding:20px;margin:12px auto;max-width:320px}
    .wide{max-width:640px}
    .label{font-size:.85em;color:#888;margin-bottom:6px}
    .val{font-size:2.8em;font-weight:bold}
    .temp-val{color:#e94560}
    .set-val{color:#4fc3f7}
    .badge{display:inline-block;padding:6px 24px;border-radius:20px;font-size:1.1em;font-weight:bold}
    .on{background:#e94560}.off{background:#333}
  </style>
</head>
<body>
  <h2>Brew Temp Controller</h2>
  <div class="card"><div class="label">Current Temperature</div><div class="val temp-val" id="temp">--</div></div>
  <div class="card"><div class="label">Set Temperature</div><div class="val set-val" id="set">--</div></div>
  <div class="card"><div class="label">Fridge</div><div class="badge off" id="state">--</div></div>
  <div class="card wide"><div class="label">24h Temperature</div><canvas id="chart"></canvas></div>
  <script src="https://cdn.jsdelivr.net/npm/chart.js@4/dist/chart.umd.min.js"></script>
  <script>
    function update(){
      fetch('/data').then(r=>r.json()).then(d=>{
        document.getElementById('temp').textContent=d.temp===null?'ERR':d.temp.toFixed(1)+'\xb0C';
        document.getElementById('set').textContent=d.set_temp+'\xb0C';
        var s=document.getElementById('state');
        s.textContent=d.fridge_on?'ON':'OFF';
        s.className='badge '+(d.fridge_on?'on':'off');
      }).catch(()=>{});
    }
    update();setInterval(update,2000);

    var chart=new Chart(document.getElementById('chart'),{
      type:'line',
      data:{labels:[],datasets:[{
        data:[],borderColor:'#e94560',backgroundColor:'rgba(233,69,96,0.1)',
        tension:0.3,pointRadius:2,borderWidth:2
      }]},
      options:{
        responsive:true,
        plugins:{legend:{display:false}},
        scales:{
          x:{ticks:{color:'#888',maxTicksLimit:8},grid:{color:'#222'}},
          y:{ticks:{color:'#888',callback:v=>v+'\xb0C'},grid:{color:'#222'}}
        }
      }
    });

    function updateChart(){
      fetch('/history').then(r=>r.json()).then(d=>{
        chart.data.labels=d.map(p=>p.t);
        chart.data.datasets[0].data=d.map(p=>p.v);
        chart.update();
      }).catch(()=>{});
    }
    updateChart();setInterval(updateChart,60000);
  </script>
</body></html>)rawliteral";
  server.send(200, "text/html", html);
}

void handleData() {
  String json = "{\"temp\":";
  if (isnan(temperature_read)) json += "null";
  else json += String(temperature_read, 1);
  json += ",\"set_temp\":" + String(set_temperature);
  json += ",\"fridge_on\":" + String(state == 1 ? "true" : "false") + "}";
  server.send(200, "application/json", json);
}

void setup(){
  Serial.begin(115200);
  delay(10);

  if(!display.begin(SSD1306_SWITCHCAPVCC, 0x3C)) { // Address 0x3D for 128x64
    Serial.println(F("SSD1306 allocation failed"));
    for(;;); 
  }
  
  readThermocouple(); //prevents startup error

  thermometer_mode(mode); //if mode == true -> showing temp only

  connect_to_wifi();

  display.clearDisplay();
  display.setTextSize(1);
  display.setTextColor(WHITE);
  display.setCursor(0, 0);
  display.print("IP: ");
  display.println(WiFi.localIP());
  display.display();
  Serial.print("IP address: ");
  Serial.println(WiFi.localIP());
  delay(3000);

  server.on("/", handleRoot);
  server.on("/data", handleData);
  server.on("/history", handleHistory);
  server.begin();
  Serial.println("Web server started");

  configTime(3600, 3600, "pool.ntp.org"); // CET/CEST (Norway)
  struct tm timeinfo;
  int ntpAttempts = 0;
  while (!getLocalTime(&timeinfo) && ntpAttempts < 10) {
    delay(500);
    ntpAttempts++;
  }
  Serial.println(ntpAttempts < 10 ? "NTP synced" : "NTP failed");

  pinMode(btn_s_down, INPUT_PULLDOWN);
  pinMode(btn_s_up, INPUT_PULLDOWN);
  attachInterrupt(btn_s_down, set_temp_decrease, FALLING);
  attachInterrupt(btn_s_up, set_temp_increase, FALLING);
}

void renderDisplay() {
  display.clearDisplay();
  display.setTextSize(2);
  display.setTextColor(WHITE);
  display.setCursor(10, 5);
  display.print("T: ");
  display.print(temperature_read, 1);
  display.print("C");
  display.setCursor(10, 25);
  display.print("S: ");
  display.print(set_temperature);
  display.print("  C");
  display.setCursor(7, 45);
  display.print(t_state);
  display.display();
}

void loop(){
  server.handleClient();

  if (set_temperature != prev_set_temperature) {
    prev_set_temperature = set_temperature;
    renderDisplay();
  }

  if (millis() - lastRead >= (unsigned long)read_time) {
    lastRead = millis();

    temperature_read = readThermocouple();
    sum += temperature_read;
    readCount++;

    Serial.print("T: ");
    Serial.println(temperature_read, 1);

    renderDisplay();

    if (readCount >= n) {
      temperature_read_avg = sum / n;
      Serial.print("avg: ");
      Serial.println(temperature_read_avg);
      temp_compare(temperature_read_avg);
      sum = 0;
      readCount = 0;
    }
  }

  if (millis() - lastHistoryRead >= HISTORY_INTERVAL) {
    lastHistoryRead = millis();
    time_t now;
    time(&now);
    if (now > 1000000000) { // only record once NTP is valid
      int writeIdx = (historyHead + historyCount) % HISTORY_SIZE;
      tempHistory[writeIdx] = temperature_read;
      timeHistory[writeIdx] = now;
      if (historyCount < HISTORY_SIZE) {
        historyCount++;
      } else {
        historyHead = (historyHead + 1) % HISTORY_SIZE; // FIFO: drop oldest
      }
    }
  }
}


void temp_compare(double temp) {
  //Easy peasy IF-statement for temperature adjust
  if (temp <= set_temperature) {
    activate_sw(fridge_off);
    delay(500);
    Serial.println("FRIDGE OFF");
    t_state = "FRIDGE OFF";
    state = 0;
  } 

  if (temp > (set_temperature /*+ dT*/)) { //for bruk med frys må systemet være mer responsiv, derfor sløfes "dT" i det tilfellet
    activate_sw(fridge_on);
    delay(500);
    Serial.println("FRIDGE ON");
    t_state = "FFRIDGE ON";
    state = 1;
  }
}

// void Post(double temp) {
  
//   int setT_int = set_temperature;
//   Serial.println(String(setT_int));
  
//   String json = "{\n \"name\": \"PNS-tempcontroll\",\n \"temp\": ";  
//   json += String (temp, 1);
//   json += ",\n \"aux_temp\": ";// "fridge" temp
//   json += String(setT_int); //får ikke omgjort volatile int til string
//   json += ",\n \"temp_unit\": \"C\", \n \"comment\": \"";
//   json += t_state;
//   json += "\" \n}";
   
//   //  HttpClient client(wifi, "log.brewfather.net", 80);

//     client.post("/stream?id=6j8S71QopX5LFK", "application/json", json);
//     //int statusCode = client.responseStatusCode();
//     //String resp = client.responseBody(); // if not called the next `.post` will return `-4`
//     client.stop();   
// }

void set_temp_decrease() {
   if (t+200<millis()){
   set_temperature --;
  Serial.println("s --"); 
  t=millis();
  }
}

void set_temp_increase() {
  if (t+200<millis()){
   set_temperature ++;
  Serial.println("s ++"); 
  t=millis();
  }
}

void activate_sw(int pin) {
  pinMode(pin,OUTPUT);
  digitalWrite(pin,HIGH);
  delay(300); // so the receiver has enough time to get the signal (?)
  digitalWrite(pin,LOW);

  Serial.print("activating: ");
  Serial.println(pin);
}

void thermometer_mode(bool mode) { //if true, unit will be "stuck" just showing the temperature
  while(mode == true) {
    temperature_read = readThermocouple();
    display.setTextSize(2);
    display.setTextColor(WHITE);
    display.setCursor(10, 5);  
    display.clearDisplay();
    display.print("T: ");
    display.print(temperature_read,1);
    display.print("C");
    delay(1000);
  }
}

void connect_to_wifi() {
  WiFi.begin(ssid, password);
  Serial.print("Connecting to WiFi");
  display.setTextSize(1);
  display.setTextColor(WHITE);
  display.setCursor(10, 10);  
  display.clearDisplay();
  display.println("Connecting to WiFi");
  display.display();
  display.setCursor(50,30);
  int i = 0;
  while (WiFi.status() != WL_CONNECTED) {
    if (i == 5){ // if the unit is not connected to wifi within 5 seconds, it restards
        ESP.restart();
    }
    else {
      Serial.print(".");
      display.print(".");
      display.display();
      delay(1000);
      i ++;
    }
  }
  display.clearDisplay();
  display.setCursor(10, 10); 
  display.print("WiFi connected!");
  display.display();
  delay(1000);
  Serial.println("IP address: ");
  Serial.println(WiFi.localIP());
}

double readThermocouple() {
    uint16_t v;
    pinMode(MAX6675_CS, OUTPUT);
    pinMode(MAX6675_SO, INPUT);
    pinMode(18, OUTPUT); // MAX6675_SCK

    digitalWrite(MAX6675_CS, LOW);
    delay(10);

    // Read in 16 bits,
    //  15    = 0 always
    //  14..2 = 0.25 degree counts MSB First
    //  2     = 1 if thermocouple is open circuit  
    //  1..0  = uninteresting status
    
    v = shiftIn(MAX6675_SO, 18, MSBFIRST);
    v <<= 8;
    v |= shiftIn(MAX6675_SO, 18, MSBFIRST);

    digitalWrite(MAX6675_CS, HIGH);
    if (v & 0x4) 
    {    
    // Bit 2 indicates if the thermocouple is disconnected
    return NAN;     
    }

    // The lower three bits (0,1,2) are discarded status bits
    v >>= 3;

    // The remaining bits are the number of 0.25 degree (C) counts
    return v*0.25-3; // measured boiling water at 102.75 degrees (C), adjusted the outputvalue
}