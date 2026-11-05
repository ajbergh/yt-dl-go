/** Translate internal failures at the presentation boundary; keep API/log details intact. */
export function userFacingMessage(message: string): string {
  if (/\bsqlite(?:_[a-z_]+|\s*(?:error|database|:|\())|\bstate database\b|\bdatabase (?:is (?:locked|busy)|schema|disk|migration|integrity|corrupt|malformed|locked|busy)/i.test(message)) {
    if (/locked|busy/i.test(message)) return "Your saved library is in use. Close any other copy of the app and try again.";
    if (/permission|access.*denied|read.?only/i.test(message)) return "The app cannot save to its data folder. Check the folder permissions and try again.";
    if (/corrupt|malformed|integrity|schema|migration/i.test(message)) return "Your saved library could not be opened. Keep a backup of your app data and restore a known-good backup before trying again.";
    return "The app could not read or save your library. Check your app data folder and try again.";
  }
  return message
    .replace(/^Native Go engine:[\s\S]*$/i, "Choose a video or audio format. Available quality depends on the video; live streams are not supported.")
    .replace(/\b(?:built-in Go|built-in|private desktop|local Go|local|native Go|Go|download) (?:service|backend)\b/gi, "app")
    .replace(/\bGo worker\b/gi, "download process")
    .replace(/\bbackend\b/gi, "app")
    .replace(/\bthe service restarts\b/gi, "the app restarts");
}
