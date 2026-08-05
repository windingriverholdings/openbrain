(function () {
  'use strict';

  window.OpenBrainComposer = {
    mount: function (options) {
      options = options || {};
      var mode = options.mode === 'search' ? 'search' : 'store';
      var host = document.getElementById(options.host || 'composer-host');
      if (!host) return;
      host.innerHTML =
        '<div id="input-bar"><div id="composer" class="' + (mode === 'search' ? 'mode-search' : '') + '" role="search" aria-label="Search memories">' +
          '<textarea id="input" rows="1" placeholder="' + (mode === 'search' ? 'What would you like to find?' : 'Message your brain…') + '" aria-label="Message input" autofocus></textarea>' +
          '<div id="composer-toolbar"><div class="composer-tools">' +
            '<span id="more-options"><button id="more-options-btn" class="composer-btn" type="button" aria-label="More options" aria-expanded="false" aria-controls="search-options" title="More options"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" stroke-linecap="round"/></svg></button>' +
              '<div id="search-options" hidden><div class="search-options-title">Search filters</div><div class="search-options-copy">Limit results to thoughts created during a specific time period.</div><div class="search-date-fields"><label class="search-date-field">From<input id="search-from" type="date" aria-label="Search from date" /></label><label class="search-date-field">To<input id="search-to" type="date" aria-label="Search to date" /></label></div><button id="search-time-reset" type="button">Search all time</button></div>' +
            '</span><span class="search-mode-wrap"><button id="search-mode" class="composer-btn" type="button" aria-pressed="' + String(mode === 'search') + '" aria-label="Toggle search mode" aria-describedby="search-mode-tooltip"><span class="mode-dot" aria-hidden="true"></span><span id="search-mode-label">' + (mode === 'search' ? 'Search' : 'Store') + '</span></button><span id="search-mode-tooltip" class="score-tooltip" role="tooltip"><span class="tooltip-title">' + (mode === 'search' ? 'Search mode' : 'Store mode') + '</span><span class="tooltip-copy">Save what you enter to your knowledge base. Press Tab to switch to Search mode.</span></span></span>' +
            '<span id="search-strategy-wrap" role="group" aria-label="Search speed and quality"><button class="strategy-option" type="button" role="radio" data-mode="vector" aria-checked="false" data-tooltip="Fast embeds your query once and searches by semantic similarity.">Fast</button><button class="strategy-option" type="button" role="radio" data-mode="hybrid" aria-checked="true" data-tooltip="Hybrid combines semantic similarity with exact keyword matching.">Hybrid</button><button class="strategy-option" type="button" role="radio" data-mode="ai" aria-checked="false" data-tooltip="AI Search chooses several safe searches, then combines their results.">AI Search</button></span>' +
            '<span id="ai-summarize-wrap" class="search-mode-wrap"><button id="ai-summarize" class="composer-btn" type="button" aria-pressed="false" aria-describedby="ai-summarize-tooltip">AI Summarize</button><span id="ai-summarize-tooltip" class="score-tooltip" role="tooltip"><span class="tooltip-title">AI Summarize</span><span class="tooltip-copy">AI Summarize explains the retrieved results using the selected search strategy.</span></span></span>' +
          '</div><button id="send" disabled aria-label="Send message" title="Send message"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M6 11l6-6 6 6"/></svg></button></div></div></div>';
      if (mode === 'search') document.getElementById('search-strategy-wrap').classList.add('visible');
      if (mode === 'search') document.getElementById('ai-summarize-wrap').classList.add('visible');
    }
  };
}());
