var StatusSelector = React.createClass({
  handleChange: function(event) {
    this.props.updateStatusFilter(event.target.value)
  },
  handleGrep: function(event) {
    this.props.updateNodeFilter(event.target.value)
  },
  render: function() {
    return (
      <div>
          <nav className="navbar navbar-default">
            <div className="container">
              <form className="form-inline">
                <button type="button" value="" className="btn btn-default navbar-btn" onClick={this.handleChange}>Any</button>
                <button type="button" value="success" className="btn btn-default navbar-btn alert-success" onClick={this.handleChange}>Success</button>
                <button type="button" value="warning" className="btn btn-default navbar-btn alert-warning" onClick={this.handleChange}>Warning</button>
                <button type="button" value="danger" className="btn btn-default navbar-btn alert-danger" onClick={this.handleChange}>Danger</button>
                <button type="button" value="info" className="btn btn-default navbar-btn alert-info" onClick={this.handleChange}>Info</button>
                <input type="text" id="nodeFilter" className="form-control" placeholder="nodename"  onKeyUp={this.handleGrep}/>
              </form>
            </div>
          </nav>
      </div>
    );
  }
});

var Title = React.createClass({
  render: function() {
    return (
      <span>: {this.props.category}</span>
    );
  }
});

var Category = React.createClass({
  render: function() {
    var active = this.props.currentCategory == this.props.name ? "active" : "";
    var href = "/" + this.props.name;
    return (
      <li role="presentation" className={active}><a href={href}>{this.props.name}</a></li>
    );
  }
});

var Categories = React.createClass({
  render: function() {
    var currentCategory = this.props.currentCategory
    var handleChange = this.handleChange
    var cats = this.props.data.map(function(cat, index) {
      return (
        <Category key={index} name={cat} currentCategory={currentCategory}/>
      );
    });
    return (
      <ul className="nav nav-tabs">
        {cats}
      </ul>
    );
  }
});

var Item = React.createClass({
  render: function() {
    var item = this.props.item;
    var icon = "glyphicon";
    var status = item.status;
    if (item.status != "success" && item.status != "info") {
      icon += " glyphicon-alert";
      status += " alert-" + item.status;
    }
    return (
      <tbody>
        <tr className={status} title={status}>
          <td><span className={icon} /> {item.node}</td>
          <td>{item.address}</td>
          <td>{item.key}</td>
          <td>{item.timestamp}</td>
        </tr>
        <tr className={status}>
          <td colSpan={4}><ItemBody>{item.data}</ItemBody></td>
        </tr>
      </tbody>
    );
  }
});

var ItemBody = React.createClass({
  handleClick: function() {
    if ( this.state.expanded && window.getSelection().toString() != "" ) {
      return;
    }
    this.setState({ expanded: !this.state.expanded })
  },
  getInitialState: function() {
    return { expanded: false };
  },
  render: function() {
    var classString = "item_body"
    if (this.state.expanded) {
      classString = "item_body_expanded"
    }
    return (
      <pre className={classString} onClick={this.handleClick}>{this.props.children}</pre>
    );
  }
});

var Dashboard = React.createClass({
  loadCategoriesFromServer: function() {
    $.ajax({
      url: "/api/?keys",
      dataType: 'json',
      success: function(data, textStatus, request) {
        if (!this.state.currentCategory) {
          location.pathname = "/" + data[0]
        } else {
          this.setState({categories: data})
        }
      }.bind(this),
      error: function(xhr, status, err) {
        console.error("/api/?keys", status, err.toString());
      }.bind(this)
    });
  },
  loadDashboardFromServer: function() {
    if (!this.state.currentCategory) {
        setTimeout(this.loadDashboardFromServer, this.props.pollWait / 5);
        return;
    }
    var statusFilter = this.state.statusFilter;
    var ajax = $.ajax({
      url: "/api/" + this.state.currentCategory + "?recurse&wait=55s&index=" + this.state.index || 0,
      dataType: 'json',
      success: function(data, textStatus, request) {
        var timer = setTimeout(this.loadDashboardFromServer, this.props.pollWait);
        var index = request.getResponseHeader('X-Consul-Index')
        this.setState({
          items: data,
          index: index,
          timer: timer,
        });
      }.bind(this),
      error: function(xhr, status, err) {
        console.log("ajax error:" + err)
        var wait = this.props.pollWait * 5
        if (err == "abort") {
          wait = 0
        }
        var timer = setTimeout(this.loadDashboardFromServer, wait);
        this.setState({ timer: timer })
      }.bind(this)
    });
    this.setState({ajax: ajax})
  },
  getInitialState: function() {
    var cat = location.pathname.replace(/^\//,"")
    if (cat == "") {
      cat = undefined
    }
    return {
      items: [],
      categories: [],
      index: 0,
      ajax: undefined,
      timer: undefined,
      statusFilter: "",
      nodeFilter: "",
      currentCategory: cat
    };
  },
  componentDidMount: function() {
    this.loadCategoriesFromServer();
    this.loadDashboardFromServer();
  },
  updateStatusFilter: function(filter) {
    this.setState({ statusFilter: filter });
  },
  updateNodeFilter: function(filter) {
    this.setState({ nodeFilter: filter });
  },
  render: function() {
    var statusFilter = this.state.statusFilter;
    var nodeFilter = this.state.nodeFilter;
    var items = this.state.items.map(function(item, index) {
      if ((statusFilter == "" || item.status == statusFilter) && (nodeFilter == "" || item.node.indexOf(nodeFilter) != -1 )) {
        return (
          <Item key={index} item={item} />
        );
      } else {
        return;
      }
    });
    return (
      <div>
        <h1>Dashboard <Title category={this.state.currentCategory} /></h1>
        <Categories data={this.state.categories} currentCategory={this.state.currentCategory} />
        <StatusSelector status={this.state.statusFilter} updateStatusFilter={this.updateStatusFilter} updateNodeFilter={this.updateNodeFilter}/>
        <table className="table table-bordered">
          <thead>
            <tr>
              <th>node</th>
              <th>address</th>
              <th>key</th>
              <th className="item_timestamp_col">timestamp</th>
            </tr>
          </thead>
          {items}
        </table>
      </div>
    );
  }
});

React.render(
  <Dashboard pollWait={1000} />,
  document.getElementById('content')
);
