package main

import "strconv"

func browserRepairPlaybackScript(positionMs, durationMs int64, expectedVideoID string, reload ...bool) string {
	reloadVideo := len(reload) > 0 && reload[0]
	return `(() => {
 const video=document.querySelector('video');
 const player=document.getElementById('movie_player');
 if (!video || !` + browserMainPlaybackScript(durationMs) + `) return false;
 const source=player && typeof player.getVideoData==='function' ? player.getVideoData()?.video_id : '';
 if (source && source!==` + strconv.Quote(expectedVideoID) + `) return false;
 const position=` + strconv.FormatFloat(float64(max(0, positionMs))/1000, 'f', 3, 64) + `;
 if (` + strconv.FormatBool(reloadVideo) + ` && player && typeof player.loadVideoById==='function') {
   const quality=typeof player.getPlaybackQuality==='function' ? player.getPlaybackQuality() : '';
   player.loadVideoById({videoId:` + strconv.Quote(expectedVideoID) + `,startSeconds:position,suggestedQuality:quality});
   if (quality && quality!=='auto') {
     try {player.setPlaybackQualityRange(quality,quality);}catch(_){}
     try {player.setPlaybackQuality(quality);}catch(_){}
   }
   return true;
 }
 if (player && typeof player.seekTo==='function') player.seekTo(position,true);
 else video.currentTime=position;
 if (player && typeof player.playVideo==='function') player.playVideo();
 else if (typeof video.play==='function') video.play().catch(()=>{});
 return true;
})()`
}
