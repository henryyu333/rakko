// shelf_np_helper - a small C bundle that reads the system Now Playing
// state through MediaRemote.framework and streams it as JSON lines on
// stdout, taking simple commands on stdin.
//
// It is NOT run on its own: since macOS 15.4 MediaRemote only answers
// clients whose code is Apple-signed, so this bundle is loaded into an
// Apple-signed host (/usr/bin/perl) by the ShelfU app; the host installs
// shelf_np_run as an xsub and calls it. Everything runs in the host
// process; the bundle never spawns one.
//
// Protocol, one JSON object per line on stdout. The line carries the raw
// MediaRemote signals; the reader reconciles them into the shown state:
//   {"ok":true,"isPlaying":<bool|null>,"appState":<int>,
//    "title":..,"artist":..,"album":..,"duration":<sec>,"elapsed":<sec>,
//    "hasRate":<bool>,"rate":<sec/sec>,"infoAt":<unix time of the info
//    dict's Timestamp, 0 when absent>,"at":<unix time>,
//    "app":<bundle id>,"artID":<track id>,"art":<base64 png>,
//    "artMissing":<bool>}
// isPlaying is null when the MediaRemote call timed out; appState is the
// MRPlaybackState of the now-playing app (-1 timeout, -2 symbol absent).
// rate and elapsed are the info dict's clock anchor as of infoAt — raw,
// never folded into the play verdict, because publishers like NetEase
// leave rate 0 in a minutes-stale dict while playback continues.
// Commands, one per line on stdin:
//   play | pause | toggle | next | prev | stop | seek <seconds> | quit
// seek moves the playing app's position; it answers {"error":"seek
// unsupported"} when this macOS build has no setter, and {"error":"bad
// seek"} for a negative, non-numeric or trailing-junk argument.
// An unknown command gets {"error":"bad command"} back, and so does a
// command line that does not fit the reader's buffer: the whole line is
// dropped, tail included, because a truncated line has no valid meaning.
// EOF on stdin is the shutdown request: the process exits after the
// current read.

#include <CoreFoundation/CoreFoundation.h>
#include <dispatch/dispatch.h>
#include <dlfcn.h>
#include <math.h>
#include <objc/message.h>
#include <objc/objc.h>
#include <pthread.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

// ---- MediaRemote symbols ------------------------------------------------

typedef void (*GetInfoFn)(dispatch_queue_t, void (^)(CFDictionaryRef));
typedef void (*GetPlayingFn)(dispatch_queue_t, void (^)(Boolean));
typedef void (*GetStateFn)(dispatch_queue_t, void (^)(int));
typedef void (*RegFn)(dispatch_queue_t);
typedef void (*SendCommandFn)(uint32_t, CFDictionaryRef);
typedef void (*GetClientFn)(dispatch_queue_t, void (^)(id));
typedef void (*SetElapsedFn)(double);

static void *MR;
static GetInfoFn MRGetInfo;
static GetPlayingFn MRGetIsPlaying;
static GetStateFn MRGetAppState;
static RegFn MRRegister;
static SendCommandFn MRSendCommand;
static GetClientFn MRGetClient;
// Optional: some macOS builds do not export the elapsed-time setter.
// Its absence only disables seek, never the rest of the helper.
static SetElapsedFn MRSetElapsed;

// MediaRemote command numbers, as in MPRemoteCommand (kMRMediaRemoteCommand).
enum {
  CMD_PLAY = 0,
  CMD_PAUSE = 1,
  CMD_TOGGLE = 2,
  CMD_STOP = 3,
  CMD_NEXT = 4,
  CMD_PREV = 5,
};

// Metadata keys, resolved from the framework (they are exported constants).
static CFStringRef KeyTitle, KeyArtist, KeyAlbum, KeyDuration, KeyElapsed,
    KeyRate, KeyArtwork, KeyItemID, KeyTimestamp;

static char *cstr(CFStringRef s) {
  if (!s) return NULL;
  CFIndex n = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s),
                                                kCFStringEncodingUTF8) + 1;
  char *buf = malloc((size_t)n);
  if (!buf) return NULL;
  if (!CFStringGetCString(s, buf, n, kCFStringEncodingUTF8)) {
    free(buf);
    return NULL;
  }
  return buf;
}

static int num(const void *v, double *out) {
  if (!v || CFGetTypeID(v) != CFNumberGetTypeID()) return 0;
  return CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, out);
}

static CFStringRef strval(CFDictionaryRef d, CFStringRef key) {
  if (!d || !key) return NULL;
  const void *v = CFDictionaryGetValue(d, key);
  if (!v || CFGetTypeID(v) != CFStringGetTypeID()) return NULL;
  return (CFStringRef)v;
}

// ---- state --------------------------------------------------------------

typedef struct {
  // isPlaying/appState are the two truth signals MediaRemote reports.
  // -1 marks a timed-out read; appState is an MRPlaybackState (1 playing,
  // 2 paused, 3 stopped, 4 interrupted, 0 unknown, -2 symbol absent).
  int isPlaying;
  int appState;
  char *title, *artist, *album, *app, *artID;
  double duration, elapsed; // elapsed is seconds at time infoAt
  int hasRate;
  double rate;    // raw PlaybackRate from the info dict
  double infoAt;  // unix time the info dict was written (its Timestamp), 0
  double at;      // unix time the snapshot was taken
  char *art;      // base64 PNG of the current sample, NULL when absent
  long artLen;
  int artMissing; // artwork lookup failed for this track
} Snapshot;
static pthread_mutex_t emitLock = PTHREAD_MUTEX_INITIALIZER;
static Snapshot cur;     // what the reader last saw
static Snapshot pending; // latest read from the system
static char *sentArtID;  // track whose artwork was already emitted
static char *seenArtID;  // current track, for the artwork grace window
static double seenArtAt; // when the current track was first sampled
static double lastEmit;  // unix time of the last emitted line
static int firstPublish = 1; // the reader always gets one line at startup
static double lastSample; // unix time of the last MediaRemote read
// Written on the stdin thread and read on the timer queue: atomic so the
// hot-poll window cannot be torn or cached across threads.
static _Atomic double cmdPollUntil;
static volatile sig_atomic_t gotNotify;

// artGrace is how long a track may carry no artwork before the reader is
// told it is missing for good: some sources publish the blob a moment
// after the metadata. A real absence surfaces as artMissing then.
static const double artGrace = 2.0;

static int stringsDiffer(const char *a, const char *b);

static double nowUnix(void) {
  struct timeval tv;
  gettimeofday(&tv, NULL);
  return (double)tv.tv_sec + (double)tv.tv_usec / 1e6;
}

static void freeSnap(Snapshot *s) {
  free(s->title); free(s->artist); free(s->album); free(s->app);
  free(s->artID); free(s->art);
  memset(s, 0, sizeof *s);
}

static void setStr(char **dst, const char *src) {
  free(*dst);
  *dst = src ? strdup(src) : NULL;
}

// ---- JSON output --------------------------------------------------------

static void putEscaped(FILE *f, const char *s) {
  fputc('"', f);
  for (; s && *s; s++) {
    unsigned char c = (unsigned char)*s;
    switch (c) {
      case '"': fputs("\\\"", f); break;
      case '\\': fputs("\\\\", f); break;
      case '\n': fputs("\\n", f); break;
      case '\r': fputs("\\r", f); break;
      case '\t': fputs("\\t", f); break;
      default:
        if (c < 0x20)
          fprintf(f, "\\u%04x", c);
        else
          fputc(c, f);
    }
  }
  fputc('"', f);
}

static const char b64[] =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

static void emitSnapshot(const Snapshot *s, int withArt) {
  pthread_mutex_lock(&emitLock);
  FILE *f = stdout;
  fputs("{\"ok\":true,\"isPlaying\":", f);
  if (s->isPlaying < 0) fputs("null", f);
  else fputs(s->isPlaying ? "true" : "false", f);
  fprintf(f, ",\"appState\":%d", s->appState);
  fputs(",\"title\":", f); putEscaped(f, s->title ? s->title : "");
  fputs(",\"artist\":", f); putEscaped(f, s->artist ? s->artist : "");
  fputs(",\"album\":", f); putEscaped(f, s->album ? s->album : "");
  fprintf(f, ",\"duration\":%.3f,\"elapsed\":%.3f,\"hasRate\":%s,"
            "\"rate\":%.3f,\"infoAt\":%.3f,\"at\":%.3f",
          s->duration, s->elapsed, s->hasRate ? "true" : "false",
          s->rate, s->infoAt, s->at);
  fputs(",\"app\":", f); putEscaped(f, s->app ? s->app : "");
  fputs(",\"artID\":", f); putEscaped(f, s->artID ? s->artID : "");
  if (s->artMissing) fputs(",\"artMissing\":true", f);
  if (withArt && s->art) {
    fputs(",\"art\":\"", f);
    fwrite(s->art, 1, (size_t)s->artLen, f);
    fputc('"', f);
  }
  fputs("}\n", f);
  fflush(f);
  pthread_mutex_unlock(&emitLock);
}

static void emitError(const char *msg) {
  pthread_mutex_lock(&emitLock);
  fputs("{\"error\":", stdout);
  putEscaped(stdout, msg ? msg : "error");
  fputs("}\n", stdout);
  fflush(stdout);
  pthread_mutex_unlock(&emitLock);
}

// ---- reading the system -------------------------------------------------

static dispatch_queue_t workQ;

// Each request carries its own semaphore: a request whose timeout has
// already passed takes no part in the next request, so a late completion
// can never desynchronize the caller. The request memory lives on the
// heap because MediaRemote may answer after the timeout; once answered or
// abandoned it is freed by whoever saw it last.
typedef struct {
  dispatch_semaphore_t done;
  CFDictionaryRef info;
  Boolean playing;
  int state; // integer answers, e.g. the app playback state
  char *text; // string answers, e.g. the client bundle id
} NPRequest;

static NPRequest *request(void) {
  NPRequest *r = calloc(1, sizeof *r);
  if (r) r->done = dispatch_semaphore_create(0);
  return r;
}

// waitRequest returns true when the request was answered before its 3 s
// timeout. On a timeout the request is abandoned to a background reaper
// that waits for the late completion — the completion still touches the
// request, so it must not be freed early.
static int waitRequest(NPRequest *r) {
  if (dispatch_semaphore_wait(
          r->done, dispatch_time(DISPATCH_TIME_NOW, 3LL * NSEC_PER_SEC)) == 0)
    return 1;
  __block NPRequest *bg = r;
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_BACKGROUND, 0), ^{
    dispatch_semaphore_wait(bg->done, DISPATCH_TIME_FOREVER);
    if (bg->info) CFRelease(bg->info);
    free(bg->text);
    dispatch_release(bg->done);
    free(bg);
  });
  return 0;
}

static void freeRequest(NPRequest *r) {
  if (r->info) CFRelease(r->info);
  free(r->text);
  dispatch_release(r->done);
  free(r);
}

// readClientBundle returns the bundle identifier of the app that owns the
// now-playing session, or NULL when MediaRemote has none to name. It is
// how the reader substitutes the source app's icon for missing artwork.
// Missing or crashing-getter tolerant: any failure just yields NULL.
static char *readClientBundle(void) {
  if (!MRGetClient) return NULL;
  NPRequest *r = request();
  if (!r) return NULL;
  MRGetClient(workQ, ^(id client) {
    if (!client) {
      dispatch_semaphore_signal(r->done);
      return;
    }
    SEL sel = sel_registerName("bundleIdentifier");
    typedef BOOL (*RespFn)(id, SEL, SEL);
    if (((RespFn)objc_msgSend)(client, sel_registerName("respondsToSelector:"),
                              sel)) {
      id bid = ((id (*)(id, SEL))objc_msgSend)(client, sel);
      if (bid) {
        const char *s = ((const char *(*)(id, SEL))objc_msgSend)(
            bid, sel_registerName("UTF8String"));
        if (s) r->text = strdup(s);
      }
    }
    dispatch_semaphore_signal(r->done);
  });
  if (!waitRequest(r)) return NULL; // abandoned; the reaper owns it now
  char *text = r->text;
  r->text = NULL;
  freeRequest(r);
  return text;
}

// readInfo fetches the current Now Playing dictionary. Returns NULL when
// nothing is playing or the read timed out.
static CFDictionaryRef readInfo(void) {
  NPRequest *r = request();
  if (!r) return NULL;
  MRGetInfo(workQ, ^(CFDictionaryRef info) {
    if (info) r->info = CFRetain(info);
    dispatch_semaphore_signal(r->done);
  });
  if (!waitRequest(r)) return NULL; // abandoned; the reaper owns it now
  CFDictionaryRef info = r->info;
  r->info = NULL;
  freeRequest(r);
  return info;
}

static int readIsPlaying(void) {
  NPRequest *r = request();
  if (!r) return -1;
  MRGetIsPlaying(workQ, ^(Boolean p) {
    r->playing = p;
    dispatch_semaphore_signal(r->done);
  });
  int playing = waitRequest(r) ? (r->playing ? 1 : 0) : -1;
  if (playing != -1) freeRequest(r);
  return playing;
}

// readAppState asks MediaRemote for the now-playing app's playback state
// (MRPlaybackState). Returns -1 on timeout, -2 when the call is absent.
static int readAppState(void) {
  if (!MRGetAppState) return -2;
  NPRequest *r = request();
  if (!r) return -1;
  MRGetAppState(workQ, ^(int s) {
    r->state = s;
    dispatch_semaphore_signal(r->done);
  });
  int state = waitRequest(r) ? r->state : -1;
  if (state != -1) freeRequest(r);
  return state;
}

// infoTimestamp converts the info dict's Timestamp to unix time. It is a
// CFDate on this system (absolute time since 2001); a CFNumber is taken
// as unix seconds when large, absolute-time seconds otherwise. 0 = absent.
static double infoTimestamp(CFDictionaryRef info) {
  if (!info || !KeyTimestamp) return 0;
  const void *v = CFDictionaryGetValue(info, KeyTimestamp);
  if (!v) return 0;
  CFTypeID t = CFGetTypeID(v);
  if (t == CFDateGetTypeID())
    return CFDateGetAbsoluteTime((CFDateRef)v) + 978307200.0;
  if (t == CFNumberGetTypeID()) {
    double n = 0;
    CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &n);
    return n >= 1e9 ? n : n + 978307200.0;
  }
  return 0;
}

static char *base64Copy(const unsigned char *data, long n) {
  long out = 4 * ((n + 2) / 3);
  char *s = malloc((size_t)out + 1);
  if (!s) return NULL;
  long o = 0;
  for (long i = 0; i < n; i += 3) {
    unsigned v = data[i] << 16;
    if (i + 1 < n) v |= data[i + 1] << 8;
    if (i + 2 < n) v |= data[i + 2];
    s[o++] = b64[(v >> 18) & 63];
    s[o++] = b64[(v >> 12) & 63];
    s[o++] = (i + 1 < n) ? b64[(v >> 6) & 63] : '=';
    s[o++] = (i + 2 < n) ? b64[v & 63] : '=';
  }
  s[o] = 0;
  return s;
}

// snapshot copies the current system state into s, freeing whatever s
// already held. Callers must freeSnap or hand it to publish afterwards.
static void snapshot(Snapshot *s) {
  freeSnap(s);
  memset(s, 0, sizeof *s);
  s->at = nowUnix();
  // The two truth signals go out raw; the reader reconciles them. They
  // can disagree briefly — IsPlaying lags some apps' pauses — so neither
  // is folded into a verdict here.
  s->isPlaying = readIsPlaying();
  s->appState = readAppState();
  // The session's owner goes out too: the reader substitutes its app
  // icon for tracks that never publish artwork.
  char *bundle = readClientBundle();
  setStr(&s->app, bundle);
  free(bundle);
  CFDictionaryRef info = readInfo();
  if (!info) return;
  char *t = cstr(strval(info, KeyTitle));
  setStr(&s->title, t);
  free(t);
  t = cstr(strval(info, KeyArtist));
  setStr(&s->artist, t);
  free(t);
  t = cstr(strval(info, KeyAlbum));
  setStr(&s->album, t);
  free(t);
  num(CFDictionaryGetValue(info, KeyDuration), &s->duration);
  num(CFDictionaryGetValue(info, KeyElapsed), &s->elapsed);
  s->hasRate = num(CFDictionaryGetValue(info, KeyRate), &s->rate);
  s->infoAt = infoTimestamp(info);

  CFStringRef item = strval(info, KeyItemID);
  t = cstr(item);
  setStr(&s->artID, t);
  free(t);
  if (!s->artID) {
    // No content item id: fall back to title+artist so a track change is
    // still visible to the reader.
    char buf[512];
    snprintf(buf, sizeof buf, "%s\n%s", s->title ? s->title : "", s->artist ? s->artist : "");
    setStr(&s->artID, buf);
  }
  // Artwork: encode it whenever this poll carries a blob the reader has
  // not been sent yet. The sent marker is checked first so the same blob
  // is never re-encoded on every poll — that was the memory the
  // published-only reads used to leak — but a blob that arrives after a
  // title-first sample is still delivered.
  const void *art = CFDictionaryGetValue(info, KeyArtwork);
  CFDataRef d = NULL;
  CFIndex artLen = 0;
  if (art && CFGetTypeID(art) == CFDataGetTypeID()) {
    d = (CFDataRef)art;
    artLen = CFDataGetLength(d);
  }
  if (d && artLen > 0 && artLen < 8 * 1024 * 1024) {
    if (stringsDiffer(sentArtID, s->artID)) {
      const UInt8 *bytes = CFDataGetBytePtr(d);
      s->art = base64Copy(bytes, (long)artLen);
      s->artLen = s->art ? (long)strlen(s->art) : 0;
    }
  } else if (d) {
    s->artMissing = 1; // empty or oversized: tell the reader to stop waiting
  } else {
    // Metadata and artwork often arrive in separate notifications (a
    // title-first sample). Wait out a short grace window before calling
    // the artwork missing for good; the reader then falls back to the
    // source app's icon.
    double now = nowUnix();
    if (!seenArtID || stringsDiffer(seenArtID, s->artID)) {
      setStr(&seenArtID, s->artID);
      seenArtAt = now;
    }
    if (now - seenArtAt >= artGrace) s->artMissing = 1;
  }
  CFRelease(info);
}

// ---- change detection and emission --------------------------------------

static int stringsDiffer(const char *a, const char *b) {
  if (!a && !b) return 0;
  if (!a || !b) return 1;
  return strcmp(a, b) != 0;
}

// publish compares the pending sample with what the reader last saw,
// updates that baseline and prints whatever the reader needs to see.
// Only the polling thread calls it.
static void publish(void) {
  double now = nowUnix();
  int trackChanged = stringsDiffer(cur.artID, pending.artID);
  // The artwork goes out exactly once per track, when the first sample
  // that carries it arrives.
  int withArt = pending.art && stringsDiffer(sentArtID, pending.artID);
  int materialChange =
      cur.isPlaying != pending.isPlaying ||
      cur.appState != pending.appState || trackChanged || withArt ||
      cur.artMissing != pending.artMissing ||
      stringsDiffer(cur.title, pending.title) ||
      stringsDiffer(cur.artist, pending.artist) ||
      stringsDiffer(cur.album, pending.album) ||
      stringsDiffer(cur.app, pending.app) ||
      (pending.duration > 0 ? cur.duration != pending.duration : 0) ||
      cur.hasRate != pending.hasRate || cur.rate != pending.rate ||
      cur.infoAt != pending.infoAt;

  // Emit on change, or once a second while playing so the reader has a
  // fresh anchor to extrapolate the progress from. A paused or idle
  // system stays silent — except for the very first line, which tells
  // the reader whether anything is playing at all.
  int anchor = (pending.isPlaying == 1 || pending.appState == 1) &&
               now - lastEmit >= 1.0;
  if (!materialChange && !anchor && !firstPublish) return;
  firstPublish = 0;

  if (withArt) {
    setStr(&sentArtID, pending.artID);
    emitSnapshot(&pending, 1);
  } else {
    emitSnapshot(&pending, 0);
  }
  freeSnap(&cur);
  cur = pending; // struct copy; ownership of the strings moves
  memset(&pending, 0, sizeof pending);
  lastEmit = now;
}

// poll is the periodic read (and the notification-triggered one).
static void poll(void) {
  lastSample = nowUnix();
  snapshot(&pending); // frees the previous unread sample first
  publish();
}

// ---- notifications ------------------------------------------------------

static void onNotification(CFNotificationCenterRef center, void *observer,
                           CFNotificationName name, const void *object,
                           CFDictionaryRef userInfo) {
  (void)center; (void)observer; (void)name; (void)object; (void)userInfo;
  gotNotify = 1;
}

static void observeNotifications(void) {
  const char *names[] = {
      "kMRMediaRemoteNowPlayingInfoDidChangeNotification",
      "kMRMediaRemoteNowPlayingApplicationIsPlayingDidChangeNotification",
      "kMRMediaRemoteNowPlayingApplicationDidChangeNotification",
      NULL};
  CFNotificationCenterRef centers[] = {
      CFNotificationCenterGetLocalCenter(),
      CFNotificationCenterGetDistributedCenter()};
  for (int ci = 0; ci < 2; ci++) {
    for (int i = 0; names[i]; i++) {
      void **sym = dlsym(MR, names[i]);
      if (!sym || !*sym) continue;
      CFNotificationCenterAddObserver(centers[ci], NULL, onNotification,
                                      (CFStringRef)*sym, NULL,
                                      CFNotificationSuspensionBehaviorDeliverImmediately);
    }
  }
}

// ---- commands -----------------------------------------------------------

static void handleCommand(const char *line, int *quit) {
  unsigned cmd;
  if (!strcmp(line, "play")) cmd = CMD_PLAY;
  else if (!strcmp(line, "pause")) cmd = CMD_PAUSE;
  else if (!strcmp(line, "toggle")) cmd = CMD_TOGGLE;
  else if (!strcmp(line, "stop")) cmd = CMD_STOP;
  else if (!strcmp(line, "next")) cmd = CMD_NEXT;
  else if (!strcmp(line, "prev")) cmd = CMD_PREV;
  else if (!strcmp(line, "quit")) { *quit = 1; return; }
  else if (!strncmp(line, "seek ", 5)) {
    // seek <seconds>: a plain decimal, nothing after it. strtod also
    // accepts "inf"; a non-finite value is rejected so the app never
    // hands MediaRemote a nonsense position.
    const char *arg = line + 5;
    char *end = NULL;
    double seconds = strtod(arg, &end);
    if (end == arg || *end != '\0' || !isfinite(seconds) || seconds < 0) {
      emitError("bad seek");
      return;
    }
    if (!MRSetElapsed) {
      emitError("seek unsupported");
      return;
    }
    MRSetElapsed(seconds);
    // Same hot-poll window as a transport command: the new position must
    // reach the reader quickly, or the progress bar snaps back.
    gotNotify = 1;
    atomic_store(&cmdPollUntil, nowUnix() + 1.5);
    return;
  } else { emitError("bad command"); return; }
  MRSendCommand(cmd, NULL);
  // The system will tell us the new state; poll soon so it lands fast.
  // The notification usually follows within a frame, but some apps are
  // slow or never post one: keep sampling hot for a short while so the
  // button and the playback state cannot disagree for long.
  gotNotify = 1;
  atomic_store(&cmdPollUntil, nowUnix() + 1.5);
}

// ---- entry --------------------------------------------------------------

static void loadMediaRemote(void) {
  MR = dlopen("/System/Library/PrivateFrameworks/MediaRemote.framework/MediaRemote",
              RTLD_NOW);
  if (!MR) {
    fprintf(stderr, "shelf-np: cannot load MediaRemote\n");
    exit(2);
  }
  MRGetInfo = (GetInfoFn)dlsym(MR, "MRMediaRemoteGetNowPlayingInfo");
  MRGetIsPlaying =
      (GetPlayingFn)dlsym(MR, "MRMediaRemoteGetNowPlayingApplicationIsPlaying");
  MRGetAppState = (GetStateFn)dlsym(
      MR, "MRMediaRemoteGetNowPlayingApplicationPlaybackState");
  MRRegister =
      (RegFn)dlsym(MR, "MRMediaRemoteRegisterForNowPlayingNotifications");
  MRSendCommand = (SendCommandFn)dlsym(MR, "MRMediaRemoteSendCommand");
  MRGetClient =
      (GetClientFn)dlsym(MR, "MRMediaRemoteGetNowPlayingClient");
  // Optional: a missing setter leaves seek unsupported, nothing else.
  MRSetElapsed = (SetElapsedFn)dlsym(MR, "MRMediaRemoteSetElapsedTime");
  KeyTitle = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoTitle");
  KeyArtist = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoArtist");
  KeyAlbum = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoAlbum");
  KeyDuration = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoDuration");
  KeyElapsed = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoElapsedTime");
  KeyRate = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoPlaybackRate");
  KeyArtwork = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoArtworkData");
  KeyItemID = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoContentItemIdentifier");
  KeyTimestamp = *(CFStringRef *)dlsym(MR, "kMRMediaRemoteNowPlayingInfoTimestamp");
  if (!MRGetInfo || !MRGetIsPlaying || !MRSendCommand) {
    fprintf(stderr, "shelf-np: MediaRemote lacks the needed functions\n");
    exit(2);
  }
}

// The reader prints nothing to stdout on its own, so a closed stdout is
// safe to ignore.
static void ignoreSigpipe(void) { signal(SIGPIPE, SIG_IGN); }

static volatile sig_atomic_t wantQuit;

// readCommandLine reads one command line into buf and strips its line
// ending. It returns 1 for a line the caller must handle, 0 at end of
// input, and 2 for a line that did not fit: the rest of that line is
// discarded and the caller answers {"error":"bad command"}.
//
// A line without its newline is never a command. fgets stops at size-1
// bytes, and the first 255 bytes of an overlong "seek <digits>" line are
// themselves a valid decimal — handing that prefix on would seek to a
// position the user never asked for, and the tail would then arrive as a
// second, bogus command.
static int readCommandLine(FILE *in, char *buf, size_t size) {
  if (!fgets(buf, (int)size, in)) return 0;
  size_t n = strlen(buf);
  if (!n || (buf[n - 1] != '\n' && buf[n - 1] != '\r')) {
    int c;
    while ((c = fgetc(in)) != EOF && c != '\n') {
    }
    return 2;
  }
  while (n && (buf[n - 1] == '\n' || buf[n - 1] == '\r')) buf[--n] = 0;
  return 1;
}

// stdinReader owns the command pipe. Commands arrive on this thread so
// the main thread stays free to run its run loop — MediaRemote
// registers this client for notifications through a main-queue
// delivery, and the distributed notification center is a run-loop
// source. A main thread blocked in fgets() never completes that
// registration and keeps reading the state it first saw (observed: the
// read side reported a stale isPlaying through whole play/pause
// cycles while a run-loop-pumping client saw every transition).
static void *stdinReader(void *unused) {
  (void)unused;
  char line[256];
  int quit = 0;
  int status;
  while (!wantQuit && (status = readCommandLine(stdin, line, sizeof line))) {
    if (status == 2) {
      emitError("bad command");
      continue;
    }
    if (!line[0]) continue;
    handleCommand(line, &quit);
    if (quit) break;
  }
  wantQuit = 1;
  // Wake the main run loop so it notices and unwinds.
  CFRunLoopStop(CFRunLoopGetMain());
  return NULL;
}

// shelf_np_run is the bundle entry the Apple-signed host calls. It runs
// until stdin is closed or "quit" arrives.
void shelf_np_run(void) {
  loadMediaRemote();
  ignoreSigpipe();
  workQ = dispatch_queue_create("shelf.np.work", NULL);
  if (MRRegister) MRRegister(dispatch_get_main_queue());
  observeNotifications();

  // First sample: the reader starts with a real state.
  poll();

  // Timer: read when the system said something changed, and at least
  // once a second (that anchor is what the reader extrapolates a moving
  // progress bar from). The 1s gate keeps an idle system down to one
  // MediaRemote round trip per second.
  dispatch_source_t timer = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, 0,
                                                   dispatch_get_global_queue(QOS_CLASS_UTILITY, 0));
  dispatch_source_set_timer(timer, dispatch_time(DISPATCH_TIME_NOW, 200 * NSEC_PER_MSEC),
                            200 * NSEC_PER_MSEC, 50 * NSEC_PER_MSEC);
  dispatch_source_set_event_handler(timer, ^{
    double now = nowUnix();
    // A command we just forwarded keeps sampling hot for a moment: the
    // app's new state must reach the reader promptly even when the app
    // neglects to post its own change notification.
    if (gotNotify || now - lastSample >= 1.0 ||
        now < atomic_load(&cmdPollUntil)) {
      gotNotify = 0;
      poll();
    }
  });
  dispatch_resume(timer);

  pthread_t rd;
  int haveReader = pthread_create(&rd, NULL, stdinReader, NULL) == 0;
  if (!haveReader) wantQuit = 1;
  // The main thread services its run loop until stdin ends: this is
  // what lets MediaRemote finish registering this client and delivers
  // now-playing notifications to the timer above.
  while (!wantQuit) {
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.2, true);
  }
  wantQuit = 1;
  CFRunLoopStop(CFRunLoopGetMain());
  if (haveReader) pthread_join(rd, NULL);
  dispatch_source_cancel(timer);
}
