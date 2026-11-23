package webgui

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// The image viewer shows the whole image when it opens, after zooming back
// to fit and after rotating, for a wide desktop screenshot and a tall image,
// in a small window and with the app's UI scale above 100% (CSS zoom on the
// root used to make the fitted image larger than the stage: cropped).
func TestImageLightboxShowsWholeImageNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	api := httptest.NewServer(srv.Handler())
	defer api.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 900, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, api.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	var ready bool
	for i := 0; i < 200 && !ready; i++ {
		_ = page.Eval(ctx, `typeof openImageLightbox==='function'&&typeof applyUI==='function'`, &ready)
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		t.Fatal("GUI did not start")
	}
	for _, scale := range []string{"normal", "large", "huge"} {
		var failure string
		script := `(async(scale)=>{try{
function check(ok,why){if(!ok)throw Error(scale+': '+why);}
ui.uiScale=scale;applyUI();
// Images are generated, not fetched: wide 2000x1076 and tall 600x1800.
var images={};
function make(name,w,h){var c=document.createElement('canvas');c.width=w;c.height=h;var g=c.getContext('2d');
  g.fillStyle='#246';g.fillRect(0,0,w,h);g.fillStyle='#f80';g.fillRect(0,0,40,40);g.fillRect(w-40,h-40,40,40);images[name]=c.toDataURL('image/png');}
make('wide.png',2000,1076);make('tall.png',600,1800);
attachmentPreviewSource=function(p){return images[p];};
async function settle(){await new Promise(r=>setTimeout(r,80));}
function whole(label){
  var box=document.querySelector('.image-lightbox'),stage=box.querySelector('.image-lightbox-stage'),img=stage.querySelector('img');
  var s=stage.getBoundingClientRect(),i=img.getBoundingClientRect(),bar=box.querySelector('.image-lightbox-toolbar').getBoundingClientRect();
  var ok=i.left>=s.left-1&&i.top>=s.top-1&&i.right<=s.right+1&&i.bottom<=s.bottom+1&&i.right<=innerWidth+1&&i.bottom<=innerHeight+1&&i.top>=bar.bottom-1;
  check(ok,label+': image '+JSON.stringify([i.left,i.top,i.right,i.bottom].map(Math.round))+' stage '+JSON.stringify([s.left,s.top,s.right,s.bottom].map(Math.round))+' window '+innerWidth+'x'+innerHeight);
  check(i.width>s.width*0.5||i.height>s.height*0.5,label+': image not fitted to the stage');
}
for (const name of ['wide.png','tall.png']) {
  openImageLightbox([name],name);
  for(var n=0;n<40&&!imageLightbox.ready;n++)await settle();
  check(imageLightbox.ready,name+' did not load');
  await settle();whole(name+' on open');
  var zoomIn=document.querySelector('[aria-label="'+t('attachment.zoomIn')+'"]');zoomIn.click();zoomIn.click();await settle();
  var fit=document.querySelector('[aria-label="'+t('attachment.fitImage')+'"]');fit.click();await settle();whole(name+' after zoom back to fit');
  document.querySelector('[aria-label="'+t('attachment.rotateImage')+'"]').click();await settle();whole(name+' after rotate');
  // Rotated a quarter turn, the fit uses the swapped sides.
  var img=document.querySelector('.image-lightbox-stage img').getBoundingClientRect();
  check(name==='wide.png'?img.height>img.width:img.width>img.height,name+' rotation did not swap the sides');
  imageLightbox.close();
}
return '';}catch(e){return e.message;}})(` + "'" + scale + "'" + `)`
		if err := page.Eval(ctx, script, &failure); err != nil {
			t.Fatal(err)
		}
		if failure != "" {
			t.Error(failure)
		}
	}
}
