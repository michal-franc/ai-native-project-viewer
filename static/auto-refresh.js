(function() {
    // Extract project prefix from URL: /p/<slug>
    var match = location.pathname.match(/^(\/p\/[^/]+)/);
    if (!match) return;
    var hashUrl = match[1] + '/hash';
    var lastHash = null;
    var interval = 3000;

    function poll() {
        fetch(hashUrl).then(function(res) {
            return res.json();
        }).then(function(data) {
            if (lastHash === null) {
                lastHash = data.hash;
            } else if (data.hash !== lastHash) {
                sessionStorage.setItem('scrollX', window.scrollX);
                sessionStorage.setItem('scrollY', window.scrollY);
                location.reload();
            }
        }).catch(function() {});
    }

    var sx = sessionStorage.getItem('scrollX');
    var sy = sessionStorage.getItem('scrollY');
    if (sx !== null && sy !== null) {
        window.scrollTo(parseInt(sx), parseInt(sy));
        sessionStorage.removeItem('scrollX');
        sessionStorage.removeItem('scrollY');
    }

    setInterval(poll, interval);
    poll();
})();
