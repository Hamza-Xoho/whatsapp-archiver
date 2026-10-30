(function () {
  function loadOriginals(root) {
    root.querySelectorAll('img[data-original]').forEach(function (img) {
      function load() {
        if (img.dataset.original) {
          img.src = img.dataset.original;
          delete img.dataset.original;
        }
      }
      if ('IntersectionObserver' in window) {
        var observer = new IntersectionObserver(function (entries) {
          if (entries[0].isIntersecting) {
            observer.disconnect();
            load();
          }
        }, {rootMargin: '300px'});
        observer.observe(img);
      } else {
        load();
      }
    });
  }

  loadOriginals(document);
  document.addEventListener('htmx:load', function (event) {
    loadOriginals(event.detail.elt || document);
  });

  var anchorID = '';
  var anchorTop = 0;
  document.addEventListener('htmx:beforeRequest', function (event) {
    var link = event.detail.elt;
    if (!link || link.dataset.edge !== 'older') return;
    var bubbles = document.querySelectorAll('#message-list .bubble');
    for (var i = 0; i < bubbles.length; i++) {
      if (bubbles[i].getBoundingClientRect().bottom > 0) {
        var heading = bubbles[i].querySelector('.meta');
        anchorID = heading ? heading.id : '';
        anchorTop = bubbles[i].getBoundingClientRect().top;
        break;
      }
    }
  });
  document.addEventListener('htmx:afterSwap', function () {
    if (!anchorID) return;
    var anchor = document.getElementById(anchorID);
    if (anchor) {
      window.scrollBy(0, anchor.closest('.bubble').getBoundingClientRect().top - anchorTop);
    }
    anchorID = '';
  });

  document.addEventListener('htmx:sseMessage', function (event) {
    if (!event.target || event.target.id !== 'new-message-stream') return;
    var updates = document.getElementById('new-message-updates');
    var markers = event.target.querySelectorAll('.new-message-marker');
    var count = parseInt(updates.dataset.count, 10) + markers.length;
    markers.forEach(function (marker) { marker.remove(); });
    updates.dataset.count = count;
    var button = document.getElementById('new-message-button');
    button.querySelector('span').textContent = count;
    button.hidden = count === 0;
  });

  var focused = window.location.hash
    ? document.getElementById(window.location.hash.slice(1))
    : document.querySelector('.msg[aria-current="true"] .meta');
  if (focused) focused.focus({preventScroll: false});
})();
